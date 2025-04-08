// Package githubapi provides a caller for the GitHub API
// to fetch repository data.
// It uses the GitHub API to fetch data about repositories
// based on the provided configuration.
// It handles authentication using an access token if provided.
// The caller is responsible for making the API request,
// decoding the response, and returning the data.

package githubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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
	TotalCount int `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items []GithubAPIResponse `json:"items"`
}

func NewCaller(logger log.Logger, config *cfg.Config, page int, perPage int) *Caller {
	return &Caller{
		Logger: logger,
		Config: config,
		Page:   page,
		PerPage: perPage,
	}
}

func (c *Caller) Call() ([]GithubAPIResponse, error) {
	// Prepare
	ctx := context.Background()
	fullUrl := fmt.Sprintf("%s&per_page=%d&page=%d", c.Config.GithubApi.ApiUrl, c.PerPage, c.Page)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullUrl, nil)
	if err != nil {
		c.Logger.Error(ctx, "Failed to create request: %v", err)
		return nil, err
	}

	// Using token for authentication
	if c.Config.GithubApi.AccessToken != "" {
		c.Logger.Info(ctx, "Using access token for authentication")
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.Config.GithubApi.AccessToken))
	} else {
		c.Logger.Warn(ctx, "No access token provided, using public API")
	}


	// Simulate API call
	rawResponse := &RawResponse{}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.Logger.Error(ctx, "Failed to make request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Logger.Error(ctx, "Failed to get response: %v", resp.Status)
		return nil, fmt.Errorf("failed to get response: %v", resp.Status)
	}

	// Decode response
	err = json.NewDecoder(resp.Body).Decode(rawResponse)
	if err != nil {
		c.Logger.Error(ctx, "Failed to decode response: %v", err)
		return nil, err
	}

	return rawResponse.Items, nil
}
