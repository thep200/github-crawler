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
	rateLimit := resp.Header.Get("X-RateLimit-Limit")
	rateRemaining := resp.Header.Get("X-RateLimit-Remaining")
	rateReset := resp.Header.Get("X-RateLimit-Reset")

	if resp.StatusCode == http.StatusForbidden && rateRemaining == "0" {
		c.Logger.Error(ctx, "Đã vượt quá giới hạn tốc độ GitHub API. Giới hạn: %s, Đặt lại: %s",
			rateLimit, rateReset)
		return nil, fmt.Errorf("vượt quá giới hạn tốc độ")
	}

	if resp.StatusCode != http.StatusOK {
		c.Logger.Error(ctx, "GitHub API trả về trạng thái không OK: %s", resp.Status)
		return nil, fmt.Errorf("không thể nhận phản hồi: %v", resp.Status)
	}

	// Ghi log thông tin giới hạn tốc độ
	if c.Page == 1 || c.Page%5 == 0 {
		c.Logger.Info(ctx, "Giới hạn tốc độ GitHub API - Còn lại: %s/%s, Đặt lại: %s",
			rateRemaining, rateLimit, rateReset)
	}

	// Giải mã phản hồi
	rawResponse := &RawResponse{}
	err = json.NewDecoder(resp.Body).Decode(rawResponse)
	if err != nil {
		c.Logger.Error(ctx, "Không thể giải mã phản hồi: %v", err)
		return nil, err
	}

	// Báo cáo tổng số trên trang đầu tiên
	if c.Page == 1 {
		c.Logger.Info(ctx, "GitHub báo cáo %d repository phù hợp tổng cộng (sẽ trả về tối đa 1000)",
			rawResponse.TotalCount)
	}

	return rawResponse.Items, nil
}
