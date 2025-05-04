# Github repository crawler

Project này crawl thông tin (name, start, ...) của các repository được public trên github.

## Start project

*   `go mod vendor`
*   `go mod tidy`
*   `go run cmd/run/main -version=v1`

## Pre-condition

Cần crawl đủ 5000 repository của github có số sao cao nhất. Các thông tin cần crawl bao gồm:
*   Tên repository
*   Số lượng sao

Rate limiting của github:
*   10 requests / 1 minute (nếu không có token)
*   30 requests / 1 minute (nếu có token)
*   Chỉ lấy được tối đa 1000 kết quả trên mỗi truy vấn

![No token got rate limiting](imgs/no-token-got-rate.png)

Rate limiting sẽ lấy theo token nếu token có được thêm vào. Nếu không có thì sẽ lấy theo IP của client. Nên cân nhắc (trade off) có sử dụng proxy để giải quyết bài toán rate limiting hay không (khi sử dụng nó thì có tốt hơn việc sử dụng token hay không).

Chúng ta có thể thêm nhiều token vào để sử dụng khi một token hết rate limiting thì chuyển sang sử dụng token khác.

## Github API

Github APIs
*   `https://api.github.com/search/repositories?q=stars:>1&sort=stars&order=desc&per_page=12` được sử dụng để lấy các thông tin cần thiết từ repo
*   `https://api.github.com/rate_limit` check rate limit

## Version growing

### V1

Crawl thông qua API search repository của github. Crawler tuần tự từng request cho tới khi hết rate limit hoặc đã crawl đủ 5000 repo có số sao cao nhất.

```mermaid
sequenceDiagram
    participant Client
    participant CrawlerV1
    participant GitHubAPI
    participant Database

    Client->>CrawlerV1: Crawl()
    activate CrawlerV1

    CrawlerV1->>Database: Begin Transaction
    activate Database

    loop Until maxRepos reached or API limit hit
        CrawlerV1->>CrawlerV1: applyRateLimit()
        CrawlerV1->>GitHubAPI: Call() - Get Repositories
        activate GitHubAPI
        GitHubAPI-->>CrawlerV1: Return Repositories
        deactivate GitHubAPI

        loop For each repository
            CrawlerV1->>CrawlerV1: crawlRepo(tx, repo)
            alt Not already processed
                CrawlerV1->>Database: Create Repository record
                CrawlerV1->>CrawlerV1: Add to processedRepoIDs

                CrawlerV1->>CrawlerV1: crawlReleases(ctx, tx, apiCaller, user, repoName, repoID)
                CrawlerV1->>CrawlerV1: applyRateLimit()
                CrawlerV1->>GitHubAPI: CallReleases(user, repoName)
                activate GitHubAPI
                GitHubAPI-->>CrawlerV1: Return Releases
                deactivate GitHubAPI

                loop For each release
                    CrawlerV1->>CrawlerV1: crawlRelease(tx, release, user, repoName, repoID)
                    alt Not already processed
                        CrawlerV1->>Database: Create Release record
                        CrawlerV1->>CrawlerV1: Add to processedReleaseKeys

                        CrawlerV1->>CrawlerV1: crawlCommits(tx, apiCaller, user, repoName, releaseID)
                        CrawlerV1->>CrawlerV1: applyRateLimit()
                        CrawlerV1->>GitHubAPI: CallCommits(user, repoName)
                        activate GitHubAPI
                        GitHubAPI-->>CrawlerV1: Return Commits
                        deactivate GitHubAPI

                        loop For each commit
                            CrawlerV1->>CrawlerV1: saveCommit(tx, commit, releaseID)
                            alt Not already processed
                                CrawlerV1->>Database: Create Commit record
                                CrawlerV1->>CrawlerV1: Add to processedCommitHashes
                            end
                        end
                    end
                end

                Note over CrawlerV1: Commit transaction
                CrawlerV1->>Database: Commit & Begin new transaction
            else Repository already processed
                CrawlerV1->>CrawlerV1: Skip repository (increment skippedRepos)
            end
        end

        CrawlerV1->>CrawlerV1: Increment page number
    end

    CrawlerV1->>Database: Final Commit Transaction
    deactivate Database

    CrawlerV1->>CrawlerV1: logCrawlResults(...)
    CrawlerV1-->>Client: Return Success/Failure
    deactivate CrawlerV1
```

### V2

### V3

## Compare

## Run command and access via `http://localhost:6060/pkg/prepuld/?m=all`

```sh
godoc -http=:6060
```
