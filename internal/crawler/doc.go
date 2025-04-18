/*
Tài liệu kỹ thuật cho GitHub Crawler Phiên bản 1

1. Tổng quan

GitHub Crawler là một công cụ được thiết kế để thu thập và lưu trữ thông tin về các repository trên GitHub.
Công cụ này sử dụng GitHub Search API để tìm kiếm các repository phổ biến nhất dựa trên số lượng sao (stars).

2. Kiến trúc

Crawler được xây dựng với cấu trúc module rõ ràng:
- github_api: Module gọi API GitHub và xử lý phản hồi
- model: Module định nghĩa cấu trúc dữ liệu và tương tác với database
- crawler: Module chính quản lý quy trình thu thập dữ liệu

3. Quy trình thu thập dữ liệu

3.1 Gọi GitHub Search API
- Crawler gọi API theo trang (pagination) để lấy danh sách repository
- Mỗi trang có thể chứa tối đa 100 mục (giới hạn của GitHub API)
- API endpoint được cấu hình qua tệp cấu hình (mặc định là repos được sắp xếp theo stars)

3.2 Xử lý phản hồi API
- Phân tích thông tin cơ bản: ID, tên, chủ sở hữu
- Thu thập các số liệu: số sao, số lượt fork, số lượt xem, số vấn đề mở

3.3 Lưu trữ dữ liệu
- Dữ liệu được lưu vào ba bảng chính: repos, releases, và commits
- Sử dụng giao dịch (transaction) để đảm bảo tính nhất quán dữ liệu
- Kiểm tra sự tồn tại trước khi chèn để tránh trùng lặp

4. Các giới hạn kỹ thuật

4.1 Giới hạn GitHub API
- Giới hạn tìm kiếm: GitHub API chỉ trả về tối đa 1000 kết quả cho mỗi truy vấn tìm kiếm
- Giới hạn tốc độ:
  * 60 yêu cầu/giờ cho người dùng không xác thực
  * 5000 yêu cầu/giờ cho người dùng đã xác thực
- Crawler có cơ chế đợi và thử lại khi đạt giới hạn tốc độ

4.2 Xử lý lỗi
- Xử lý lỗi kết nối mạng
- Xử lý giới hạn tốc độ API và thử lại
- Hoàn tác (rollback) giao dịch cơ sở dữ liệu nếu xảy ra lỗi

5. Tối ưu hóa hiệu suất

- Số lượng mục tối đa trên mỗi trang: 100 (giới hạn của GitHub API)
- Commit sớm: Thực hiện commit sau mỗi 5 trang để tránh giao dịch dài
- Độ trễ động: Điều chỉnh độ trễ giữa các yêu cầu dựa trên trạng thái xác thực
- Phát hiện kết thúc dữ liệu: Dừng khi nhận nhiều trang trống liên tiếp

6. Hướng dẫn sử dụng

- Cấu hình API URL trong tệp cấu hình để thay đổi tiêu chí tìm kiếm
- Cung cấp GitHub API token (nếu có) để tăng giới hạn tốc độ
- Chạy ứng dụng từ main.go

7. Cải tiến trong tương lai

- Hỗ trợ nhiều loại query tìm kiếm
- Thu thập thông tin chi tiết hơn (READMEs, languages, contributors)
- Cơ chế cập nhật thông tin repository theo định kỳ
- Tăng khả năng chịu lỗi và cơ chế phục hồi
*/

package crawler
