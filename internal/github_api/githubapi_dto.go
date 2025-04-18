// Gói dto cung cấp các đối tượng truyền dữ liệu cho dự án
// Chuyển đổi phản hồi api tìm kiếm github thành một cấu trúc

package githubapi

import "time"

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
}

// ReleaseResponse đại diện cho phản hồi API release của GitHub
type ReleaseResponse struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
}

// CommitResponse đại diện cho phản hồi API commit của GitHub
type CommitResponse struct {
	SHA     string       `json:"sha"`
	Commit  CommitDetail `json:"commit"`
	HTMLURL string       `json:"html_url"`
}

// CommitDetail chứa thông tin chi tiết về commit
type CommitDetail struct {
	Author    CommitAuthor `json:"author"`
	Committer CommitAuthor `json:"committer"`
	Message   string       `json:"message"`
}

// CommitAuthor chứa thông tin về tác giả commit
type CommitAuthor struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}
