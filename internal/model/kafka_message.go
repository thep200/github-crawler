package model

// RepoMessage là cấu trúc dữ liệu Repository gửi tới Kafka
type RepoMessage struct {
	ID         int    `json:"id"`
	User       string `json:"user"`
	Name       string `json:"name"`
	StarCount  int    `json:"star_count"`
	ForkCount  int    `json:"fork_count"`
	WatchCount int    `json:"watch_count"`
	IssueCount int    `json:"issue_count"`
}

// ReleaseMessage là cấu trúc dữ liệu Release gửi tới Kafka
type ReleaseMessage struct {
	Content string `json:"content"`
	RepoID  int    `json:"repo_id"`
}

// CommitMessage là cấu trúc dữ liệu Commit gửi tới Kafka
type CommitMessage struct {
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	ReleaseID int    `json:"release_id"`
}
