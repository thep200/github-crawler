# Github repository crawler

Project này crawl thông tin (name, start, ...) của các repository được public trên github.

## Start project

*   `go mod install`
*   `go mod vendor`
*   `go run cmd/run/main`


## Pre-condition

Cần crawl đủ 5000 repository của github có số sao cao nhất. Các thông tin cần crawl bao gồm:
*   Tên repository
*   Số lượng sao

Rate limiting của github:
*   60 requests / 1 hour (nếu không có token)
*   5000 requests / 1 hour (nếu có token)

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

### V2

Cải tiến
*   Thêm các worker để xử lý bất đồng bộ thay vì xử lý tuần tự (chú ý rate limiting)

### V3

Cải tiến
*   Queue
*   Auto scale woker, comsumer


## Compare

Chú ý so sánh các các lựa chọn thực hiện. Ví dụ: Tại sao lại chọn token thay vì proxy?, các technical để vượt qua rate limiting (proxy hay thêm các token)?. Các kỹ thuật xử lý rate limiting.


