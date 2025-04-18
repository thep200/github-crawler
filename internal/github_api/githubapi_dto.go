// Gói dto cung cấp các đối tượng truyền dữ liệu cho dự án
// Chuyển đổi phản hồi api tìm kiếm github thành một cấu trúc

package githubapi

type Owner struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

type GithubAPIResponse struct {
	Id              int64  `json:"id"`
	Name            string `json:"name"`
	FullName        string `json:"full_name"`
	Owner           Owner  `json:"owner"`
	StargazersCount int64  `json:"stargazers_count"`
	ForksCount      int64  `json:"forks_count"`
	WatchersCount   int64  `json:"watchers_count"`
	OpenIssuesCount int64  `json:"open_issues_count"`
	// Có thể thêm nhiều trường tại đây
}
