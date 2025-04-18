// Gói githubapi cung cấp một caller cho GitHub API
// để lấy dữ liệu repository.
// Nó sử dụng GitHub API để lấy dữ liệu về các repository
// dựa trên cấu hình được cung cấp.
// Nó xử lý xác thực bằng mã thông báo truy cập nếu được cung cấp.
// Caller chịu trách nhiệm thực hiện yêu cầu API,
// giải mã phản hồi và trả về dữ liệu.

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
	// Chuẩn bị
	ctx := context.Background()
	fullUrl := fmt.Sprintf("%s&per_page=%d&page=%d", c.Config.GithubApi.ApiUrl, c.PerPage, c.Page)

	// Tạo request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullUrl, nil)
	if err != nil {
		c.Logger.Error(ctx, "Không thể tạo request: %v", err)
		return nil, err
	}

	// Thiết lập header phổ biến
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Sử dụng token cho xác thực
	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	}

	// Thực hiện request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		c.Logger.Error(ctx, "Không thể thực hiện request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	// Kiểm tra giới hạn tốc độ
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")

	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		return nil, fmt.Errorf("vượt quá giới hạn tốc độ")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("không thể nhận phản hồi: %v", resp.Status)
	}

	// Giải mã phản hồi
	rawResponse := &RawResponse{}
	err = json.NewDecoder(resp.Body).Decode(rawResponse)
	if err != nil {
		return nil, err
	}

	return rawResponse.Items, nil
}

// CallReleases gọi API releases của GitHub cho một repository cụ thể
func (c *Caller) CallReleases(user, repo string) ([]ReleaseResponse, error) {
	ctx := context.Background()

	// Thay thế placeholders trong URL template
	releasesUrl := strings.ReplaceAll(c.Config.GithubApi.ReleasesApiUrl, "{user}", user)
	releasesUrl = strings.ReplaceAll(releasesUrl, "{repo}", repo)

	// Tạo request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesUrl, nil)
	if err != nil {
		return nil, err
	}

	// Thiết lập headers
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	}

	// Thực hiện request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Kiểm tra giới hạn tốc độ
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		return nil, fmt.Errorf("vượt quá giới hạn tốc độ")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("không thể nhận phản hồi: %v", resp.Status)
	}

	// Giải mã phản hồi
	var releases []ReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

// CallCommits gọi API commits của GitHub cho một repository cụ thể
func (c *Caller) CallCommits(user, repo string) ([]CommitResponse, error) {
	ctx := context.Background()

	// Thay thế placeholders trong URL template
	commitsUrl := strings.ReplaceAll(c.Config.GithubApi.CommitsApiUrl, "{user}", user)
	commitsUrl = strings.ReplaceAll(commitsUrl, "{repo}", repo)

	// Tạo request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commitsUrl, nil)
	if err != nil {
		return nil, err
	}

	// Thiết lập headers
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.Config.GithubApi.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	}

	// Thực hiện request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Kiểm tra giới hạn tốc độ
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		return nil, fmt.Errorf("vượt quá giới hạn tốc độ")
	}

	if resp.StatusCode == 404 {
		// Repository không có commit nào
		return []CommitResponse{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("không thể nhận phản hồi: %v", resp.Status)
	}

	// Giải mã phản hồi
	var commits []CommitResponse
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}

	return commits, nil
}
