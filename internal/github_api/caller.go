// Gói githubapi cung cấp một caller cho GitHub API, để lấy dữ liệu repository.
// Nó sử dụng GitHub API để lấy dữ liệu về các repository, dựa trên cấu hình được cung cấp.
// Nó xử lý xác thực bằng mã thông báo truy cập nếu được cung cấp.
// Caller chịu trách nhiệm thực hiện yêu cầu API

package githubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/log"
)

type Caller struct {
	Logger         log.Logger
	Config         *cfg.Config
	Page           int
	PerPage        int
	originalToken  string
	usingToken     bool
	tokenDisabled  bool
	tokenDisableAt time.Time
}

// Mapping response
type RawResponse struct {
	TotalCount        int                 `json:"total_count"`
	IncompleteResults bool                `json:"incomplete_results"`
	Items             []GithubAPIResponse `json:"items"`
}

func NewCaller(logger log.Logger, config *cfg.Config, page int, perPage int) *Caller {
	return &Caller{
		Logger:        logger,
		Config:        config,
		Page:          page,
		PerPage:       perPage,
		originalToken: config.GithubApi.AccessToken,
		usingToken:    config.GithubApi.AccessToken != "",
		tokenDisabled: false,
	}
}

func (c *Caller) HandleRateLimit(ctx context.Context, resp *http.Response) (bool, error) {
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")

	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		resetTimeStr := resp.Header.Get("X-RateLimit-Reset")
		resetTimeInt, err := strconv.ParseInt(resetTimeStr, 10, 64)

		if err != nil {
			waitTime := time.Duration(c.Config.GithubApi.RateLimitResetMin) * time.Minute
			c.toggleToken(ctx) // Toggle token state
			return true, fmt.Errorf("ratelimit hit, chờ %v", waitTime)
		}

		//
		resetTime := time.Unix(resetTimeInt, 0)
		now := time.Now()
		waitTime := resetTime.Sub(now)

		// Chờ thêm sau khi qua thời gian reset
		if waitTime < 0 {
			waitTime = time.Duration(c.Config.GithubApi.RateLimitResetMin) * time.Minute
		}

		c.Logger.Warn(
			ctx,
			"Rate limit hit. Cần chờ %v đến %v để tiếp tục. Toggling token usage.",
			waitTime.Round(time.Second), resetTime.Format(time.RFC3339),
		)

		c.toggleToken(ctx) // Toggle token state
		return true, fmt.Errorf("ratelimit API hit, thời gian reset: %v", resetTime.Format(time.RFC3339))
	}

	return false, nil
}

// toggleToken switches between using token and not using token
func (c *Caller) toggleToken(ctx context.Context) {
	if c.originalToken == "" {
		// We don't have a token to toggle
		return
	}

	if c.usingToken {
		// Disable token usage
		c.usingToken = false
		c.tokenDisabled = true
		c.tokenDisableAt = time.Now()
		c.Logger.Info(ctx, "Token disabled due to rate limiting")
	} else {
		// Re-enable token usage
		c.usingToken = true
		c.tokenDisabled = false
		c.Logger.Info(ctx, "Token re-enabled after rate limiting")
	}
}

// getAuthHeader returns the authorization header value based on current token state
func (c *Caller) getAuthHeader() string {
	if c.usingToken && c.originalToken != "" {
		return fmt.Sprintf("token %s", c.originalToken)
	}
	return ""
}

func (c *Caller) Call() ([]GithubAPIResponse, error) {
	ctx := context.Background()
	baseUrl := c.Config.GithubApi.ApiUrl
	if !strings.Contains(baseUrl, "sort=stars") {
		if strings.Contains(baseUrl, "?") {
			baseUrl += "&sort=stars&order=desc"
		} else {
			baseUrl += "?sort=stars&order=desc"
		}
	}

	fullUrl := fmt.Sprintf("%s&per_page=%d&page=%d", baseUrl, c.PerPage, c.Page)
	c.Logger.Info(ctx, "Calling GitHub API: %s", fullUrl)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullUrl, nil)
	if err != nil {
		c.Logger.Error(ctx, "Cannot request: %v", err)
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use current token state
	authHeader := c.getAuthHeader()
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
		c.Logger.Debug(ctx, "Using token for request")
	} else {
		c.Logger.Debug(ctx, "Not using token for request")
	}

	// Thực hiện request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		c.Logger.Error(ctx, "cannot send request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	c.Logger.Info(ctx, "Rate limit remaining: %s", rateRemaining)

	// Kiểm tra rate limit
	isRateLimited, rateLimitErr := c.HandleRateLimit(ctx, resp)
	if isRateLimited {
		return nil, rateLimitErr
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Call failed: %v", resp.Status)
	}

	//
	rawResponse := &RawResponse{}
	err = json.NewDecoder(resp.Body).Decode(rawResponse)
	if err != nil {
		return nil, err
	}

	return rawResponse.Items, nil
}

func (c *Caller) CallReleases(user, repo string) ([]ReleaseResponse, error) {
	ctx := context.Background()

	//
	releasesUrl := strings.ReplaceAll(c.Config.GithubApi.ReleasesApiUrl, "{user}", user)
	releasesUrl = strings.ReplaceAll(releasesUrl, "{repo}", repo)

	//
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesUrl, nil)
	if err != nil {
		return nil, err
	}

	//
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use current token state
	authHeader := c.getAuthHeader()
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	//
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Kiểm tra rate limit
	isRateLimited, rateLimitErr := c.HandleRateLimit(ctx, resp)
	if isRateLimited {
		return nil, rateLimitErr
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot received response: %v", resp.Status)
	}

	//
	var releases []ReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

func (c *Caller) CallCommits(user, repo string) ([]CommitResponse, error) {
	ctx := context.Background()

	//
	commitsUrl := strings.ReplaceAll(c.Config.GithubApi.CommitsApiUrl, "{user}", user)
	commitsUrl = strings.ReplaceAll(commitsUrl, "{repo}", repo)

	//
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commitsUrl, nil)
	if err != nil {
		return nil, err
	}

	//
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use current token state
	authHeader := c.getAuthHeader()
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	//
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Kiểm tra rate limit
	isRateLimited, rateLimitErr := c.HandleRateLimit(ctx, resp)
	if isRateLimited {
		return nil, rateLimitErr
	}

	if resp.StatusCode == 404 {
		return []CommitResponse{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot received response: %v", resp.Status)
	}

	//
	var commits []CommitResponse
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}

	return commits, nil
}
