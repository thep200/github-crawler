#

## API get repo
https://api.github.com/search/repositories

```json
  {
    "total_count": 1234,
    "incomplete_results": false,
    "items": [
      {
        "id": 123456,
        "name": "repo-name",
        "full_name": "owner/repo-name",
        "owner": {
          "login": "owner",
          "id": 789
        },
        "html_url": "https://github.com/owner/repo-name",
        "description": "Mô tả repository",
        "stargazers_count": 100,
        "forks_count": 20,
        "updated_at": "2025-04-17T10:00:00Z"
      },
      ...
    ]
  }
```

## API get release
https://api.github.com/repos/{user}/{repo}/releases

Response
```json
[
  {
    "id": 123456,
    "tag_name": "v7.0.0",
    "name": "Rails 7.0.0",
    "created_at": "2025-04-01T10:00:00Z",
    "published_at": "2025-04-01T12:00:00Z",
    "body": "Ghi chú phát hành cho Rails 7.0.0...",
    "html_url": "https://github.com/rails/rails/releases/tag/v7.0.0",
    "assets": [
      {
        "name": "rails-7.0.0.zip",
        "size": 102400,
        "download_count": 100,
        "browser_download_url": "https://github.com/rails/rails/releases/download/v7.0.0/rails-7.0.0.zip"
      }
    ]
  },
  ...
]
```

## API get commit
https://api.github.com/repos/{user}/{repo}/commits

```json
[
  {
    "sha": "abc123...",
    "commit": {
      "author": {
        "name": "Tên tác giả",
        "email": "author@example.com",
        "date": "2025-04-01T12:00:00Z"
      },
      "message": "Thêm tính năng mới cho Rails",
      "tree": {
        "sha": "def456...",
        "url": "https://api.github.com/repos/rails/rails/git/trees/def456..."
      }
    },
    "html_url": "https://github.com/rails/rails/commit/abc123...",
    "author": {
      "login": "author-login",
      "id": 789
    }
  },
  ...
```


