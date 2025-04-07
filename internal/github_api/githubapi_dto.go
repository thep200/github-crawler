// Package dto provides data transfer objects for the project
// Transfer github search api response into a struct

package githubapi

type GithubAPIResponse struct {
	Id              int64  `json:"id"`
	Name            string `json:"name"`
	StargazersCount int64  `json:"stargazers_count"`
	// Can more fields be added here
}
