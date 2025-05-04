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
	"strings"
	"time"

	"github.com/thep200/github-crawler/cfg"
	"github.com/thep200/github-crawler/pkg/log"
)

type Caller struct {
	Logger  log.Logger
	Config  *cfg.Config
	Page    int
	PerPage int
}

// Mapping response
type RawResponse struct {
	TotalCount        int                 `json:"total_count"`
	IncompleteResults bool                `json:"incomplete_results"`
	Items             []GithubAPIResponse `json:"items"`
}

func NewCaller(logger log.Logger, config *cfg.Config, page int, perPage int) *Caller {
	return &Caller{
		Logger:  logger,
		Config:  config,
		Page:    page,
		PerPage: perPage,
	}
}

func (c *Caller) Call() ([]GithubAPIResponse, error) {
	ctx := context.Background()

	// Ensure the API URL has the correct sort parameters
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

	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
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

	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		resetTime := resp.Header.Get("X-RateLimit-Reset")
		return nil, fmt.Errorf("rate limit exceeded, resets at %s", resetTime)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot received response: %v", resp.Status)
	}

	// Giải mã phản hồi
	rawResponse := &RawResponse{}
	err = json.NewDecoder(resp.Body).Decode(rawResponse)
	if err != nil {
		return nil, err
	}

	c.Logger.Info(ctx, "Total repositories found: %d, page: %d, items received: %d",
		rawResponse.TotalCount, c.Page, len(rawResponse.Items))

	if c.Page*c.PerPage > 1000 {
		c.Logger.Warn(ctx, "GitHub API only provides access to the first 1,000 search results")
	}

	return rawResponse.Items, nil
}

// CallReleases gọi API releases của GitHub cho một repository cụ thể
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
	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	}

	//
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		return nil, fmt.Errorf("rate limit")
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
	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	}

	//
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		return nil, fmt.Errorf("rate limit")
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
