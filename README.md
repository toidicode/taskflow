# TaskFlow

Code của series **[Học Golang](https://toidicode.com/series/hoc-golang)** trên [toidicode.com](https://toidicode.com): 53 bài, từ "Hello, World" tới một REST API quản lý task chạy với PostgreSQL, Redis, gRPC, OpenTelemetry và CI/CD.

Mỗi bài là một commit, đánh dấu bằng tag `bai-NN`. Muốn xem code đúng như ở cuối một bài:

```bash
git clone https://github.com/toidicode/taskflow.git
cd taskflow
git checkout bai-30
```

Cột "Thay đổi" mở diff của riêng bài đó so với bài trước. Bài không có link là bài chỉ dùng ví dụ nhỏ, không sửa project.

Chạy nhanh (cần Docker): xem `deploy/compose.yaml` và hướng dẫn trong Bài 39, 53.

## Phần 1: Ngôn ngữ Go (thư viện chuẩn, CLI quản lý task)

| Bài | Tiêu đề | Code | Thay đổi |
|---:|---|---|---|
| 1 | [Giới thiệu Golang và cài đặt Go](https://toidicode.com/gioi-thieu-golang-va-cai-dat-go-524.html) |  |  |
| 2 | [Chương trình Go đầu tiên và Go module](https://toidicode.com/chuong-trinh-go-dau-tien-va-go-module-525.html) | [`bai-02`](https://github.com/toidicode/taskflow/tree/bai-02) |  |
| 3 | [Biến, hằng số và kiểu dữ liệu trong Go](https://toidicode.com/bien-hang-so-va-kieu-du-lieu-trong-go-526.html) |  |  |
| 4 | [Câu lệnh điều kiện và vòng lặp trong Go](https://toidicode.com/cau-lenh-dieu-kien-va-vong-lap-trong-go-527.html) |  |  |
| 5 | [Hàm trong Go](https://toidicode.com/ham-trong-go-540.html) |  |  |
| 6 | [Array, slice và map trong Go](https://toidicode.com/array-slice-va-map-trong-go-541.html) |  |  |
| 7 | [String, rune và byte trong Go](https://toidicode.com/string-rune-va-byte-trong-go-542.html) |  |  |
| 8 | [Struct và method trong Go](https://toidicode.com/struct-va-method-trong-go-543.html) | [`bai-08`](https://github.com/toidicode/taskflow/tree/bai-08) | [diff](https://github.com/toidicode/taskflow/compare/bai-07...bai-08) |
| 9 | [Con trỏ trong Go](https://toidicode.com/con-tro-trong-go-552.html) | [`bai-09`](https://github.com/toidicode/taskflow/tree/bai-09) | [diff](https://github.com/toidicode/taskflow/compare/bai-08...bai-09) |
| 10 | [Interface trong Go](https://toidicode.com/interface-trong-go-553.html) | [`bai-10`](https://github.com/toidicode/taskflow/tree/bai-10) | [diff](https://github.com/toidicode/taskflow/compare/bai-09...bai-10) |
| 11 | [Xử lý lỗi trong Go](https://toidicode.com/xu-ly-loi-trong-go-554.html) | [`bai-11`](https://github.com/toidicode/taskflow/tree/bai-11) | [diff](https://github.com/toidicode/taskflow/compare/bai-10...bai-11) |
| 12 | [Làm việc với file và I/O trong Go](https://toidicode.com/lam-viec-voi-file-va-io-trong-go-555.html) | [`bai-12`](https://github.com/toidicode/taskflow/tree/bai-12) | [diff](https://github.com/toidicode/taskflow/compare/bai-11...bai-12) |
| 13 | [Xử lý thời gian với package time trong Go](https://toidicode.com/xu-ly-thoi-gian-voi-package-time-trong-go-560.html) | [`bai-13`](https://github.com/toidicode/taskflow/tree/bai-13) | [diff](https://github.com/toidicode/taskflow/compare/bai-12...bai-13) |
| 14 | [Package và quản lý dependency trong Go](https://toidicode.com/package-va-quan-ly-dependency-trong-go-561.html) | [`bai-14`](https://github.com/toidicode/taskflow/tree/bai-14) | [diff](https://github.com/toidicode/taskflow/compare/bai-13...bai-14) |
| 15 | [Generics trong Go](https://toidicode.com/generics-trong-go-562.html) | [`bai-15`](https://github.com/toidicode/taskflow/tree/bai-15) | [diff](https://github.com/toidicode/taskflow/compare/bai-14...bai-15) |
| 16 | [Iterator trong Go](https://toidicode.com/iterator-trong-go-563.html) | [`bai-16`](https://github.com/toidicode/taskflow/tree/bai-16) | [diff](https://github.com/toidicode/taskflow/compare/bai-15...bai-16) |
| 17 | [Goroutine và channel trong Go](https://toidicode.com/goroutine-va-channel-trong-go-573.html) |  |  |
| 18 | [Select, sync và context trong Go](https://toidicode.com/select-sync-va-context-trong-go-574.html) | [`bai-18`](https://github.com/toidicode/taskflow/tree/bai-18) | [diff](https://github.com/toidicode/taskflow/compare/bai-17...bai-18) |
| 19 | [Các mẫu xử lý đồng thời trong Go](https://toidicode.com/cac-mau-xu-ly-dong-thoi-trong-go-575.html) |  |  |
| 20 | [Testing trong Go](https://toidicode.com/testing-trong-go-576.html) | [`bai-20`](https://github.com/toidicode/taskflow/tree/bai-20) | [diff](https://github.com/toidicode/taskflow/compare/bai-19...bai-20) |
| 21 | [Reflection và struct tag trong Go](https://toidicode.com/reflection-va-struct-tag-trong-go-585.html) |  |  |
| 22 | [Xây dựng ứng dụng CLI với Go](https://toidicode.com/xay-dung-ung-dung-cli-voi-go-586.html) | [`bai-22`](https://github.com/toidicode/taskflow/tree/bai-22) | [diff](https://github.com/toidicode/taskflow/compare/bai-21...bai-22) |

## Phần 2: REST API TaskFlow với thư viện chuẩn, pgx, goose, sqlc

| Bài | Tiêu đề | Code | Thay đổi |
|---:|---|---|---|
| 23 | [Xây dựng HTTP server với net/http](https://toidicode.com/xay-dung-http-server-voi-nethttp-587.html) | [`bai-23`](https://github.com/toidicode/taskflow/tree/bai-23) | [diff](https://github.com/toidicode/taskflow/compare/bai-22...bai-23) |
| 24 | [Routing trong Go với ServeMux](https://toidicode.com/routing-trong-go-voi-servemux-588.html) | [`bai-24`](https://github.com/toidicode/taskflow/tree/bai-24) | [diff](https://github.com/toidicode/taskflow/compare/bai-23...bai-24) |
| 25 | [JSON và REST API trong Go](https://toidicode.com/json-va-rest-api-trong-go-593.html) | [`bai-25`](https://github.com/toidicode/taskflow/tree/bai-25) | [diff](https://github.com/toidicode/taskflow/compare/bai-24...bai-25) |
| 26 | [Middleware trong Go](https://toidicode.com/middleware-trong-go-594.html) | [`bai-26`](https://github.com/toidicode/taskflow/tree/bai-26) | [diff](https://github.com/toidicode/taskflow/compare/bai-25...bai-26) |
| 27 | [Validate dữ liệu request trong Go](https://toidicode.com/validate-du-lieu-request-trong-go-595.html) | [`bai-27`](https://github.com/toidicode/taskflow/tree/bai-27) | [diff](https://github.com/toidicode/taskflow/compare/bai-26...bai-27) |
| 28 | [Cấu hình và logging với slog trong Go](https://toidicode.com/cau-hinh-va-logging-voi-slog-trong-go-596.html) | [`bai-28`](https://github.com/toidicode/taskflow/tree/bai-28) | [diff](https://github.com/toidicode/taskflow/compare/bai-27...bai-28) |
| 29 | [Kết nối PostgreSQL trong Go](https://toidicode.com/ket-noi-postgresql-trong-go-601.html) | [`bai-29`](https://github.com/toidicode/taskflow/tree/bai-29) | [diff](https://github.com/toidicode/taskflow/compare/bai-28...bai-29) |
| 30 | [Migration database với goose trong Go](https://toidicode.com/migration-database-voi-goose-trong-go-602.html) | [`bai-30`](https://github.com/toidicode/taskflow/tree/bai-30) | [diff](https://github.com/toidicode/taskflow/compare/bai-29...bai-30) |
| 31 | [Sinh code truy vấn với sqlc trong Go](https://toidicode.com/sinh-code-truy-van-voi-sqlc-trong-go-603.html) | [`bai-31`](https://github.com/toidicode/taskflow/tree/bai-31) | [diff](https://github.com/toidicode/taskflow/compare/bai-30...bai-31) |
| 32 | [Transaction và quan hệ dữ liệu trong Go](https://toidicode.com/transaction-va-quan-he-du-lieu-trong-go-604.html) | [`bai-32`](https://github.com/toidicode/taskflow/tree/bai-32) | [diff](https://github.com/toidicode/taskflow/compare/bai-31...bai-32) |
| 33 | [Đăng ký, đăng nhập và JWT trong Go](https://toidicode.com/dang-ky-dang-nhap-va-jwt-trong-go-609.html) | [`bai-33`](https://github.com/toidicode/taskflow/tree/bai-33) | [diff](https://github.com/toidicode/taskflow/compare/bai-32...bai-33) |
| 34 | [Phân quyền trong Go](https://toidicode.com/phan-quyen-trong-go-610.html) | [`bai-34`](https://github.com/toidicode/taskflow/tree/bai-34) | [diff](https://github.com/toidicode/taskflow/compare/bai-33...bai-34) |
| 35 | [Upload file trong Go](https://toidicode.com/upload-file-trong-go-611.html) | [`bai-35`](https://github.com/toidicode/taskflow/tree/bai-35) | [diff](https://github.com/toidicode/taskflow/compare/bai-34...bai-35) |
| 36 | [Background job trong Go](https://toidicode.com/background-job-trong-go-612.html) | [`bai-36`](https://github.com/toidicode/taskflow/tree/bai-36) | [diff](https://github.com/toidicode/taskflow/compare/bai-35...bai-36) |
| 37 | [Test HTTP handler và integration test trong Go](https://toidicode.com/test-http-handler-va-integration-test-trong-go-613.html) | [`bai-37`](https://github.com/toidicode/taskflow/tree/bai-37) | [diff](https://github.com/toidicode/taskflow/compare/bai-36...bai-37) |
| 38 | [Graceful shutdown, build và Docker cho ứng dụng Go](https://toidicode.com/graceful-shutdown-build-va-docker-cho-ung-dung-go-614.html) | [`bai-38`](https://github.com/toidicode/taskflow/tree/bai-38) | [diff](https://github.com/toidicode/taskflow/compare/bai-37...bai-38) |
| 39 | [Deploy ứng dụng Go lên VPS](https://toidicode.com/deploy-ung-dung-go-len-vps-615.html) | [`bai-39`](https://github.com/toidicode/taskflow/tree/bai-39) | [diff](https://github.com/toidicode/taskflow/compare/bai-38...bai-39) |

## Phần 3: Nâng cao và hệ sinh thái Go (Gin, Redis, WebSocket, gRPC, observability, CI/CD)

| Bài | Tiêu đề | Code | Thay đổi |
|---:|---|---|---|
| 40 | [Cấu trúc dự án Go lớn](https://toidicode.com/cau-truc-du-an-go-lon-616.html) | [`bai-40`](https://github.com/toidicode/taskflow/tree/bai-40) | [diff](https://github.com/toidicode/taskflow/compare/bai-39...bai-40) |
| 41 | [Gin framework trong Go](https://toidicode.com/gin-framework-trong-go-617.html) | [`bai-41`](https://github.com/toidicode/taskflow/tree/bai-41) | [diff](https://github.com/toidicode/taskflow/compare/bai-40...bai-41) |
| 42 | [GORM trong Go](https://toidicode.com/gorm-trong-go-618.html) |  |  |
| 43 | [Cache với Redis trong Go](https://toidicode.com/cache-voi-redis-trong-go-619.html) | [`bai-43`](https://github.com/toidicode/taskflow/tree/bai-43) | [diff](https://github.com/toidicode/taskflow/compare/bai-42...bai-43) |
| 44 | [Rate limiting trong Go](https://toidicode.com/rate-limiting-trong-go-620.html) | [`bai-44`](https://github.com/toidicode/taskflow/tree/bai-44) | [diff](https://github.com/toidicode/taskflow/compare/bai-43...bai-44) |
| 45 | [Tài liệu API với OpenAPI trong Go](https://toidicode.com/tai-lieu-api-voi-openapi-trong-go-621.html) | [`bai-45`](https://github.com/toidicode/taskflow/tree/bai-45) | [diff](https://github.com/toidicode/taskflow/compare/bai-44...bai-45) |
| 46 | [WebSocket trong Go](https://toidicode.com/websocket-trong-go-622.html) | [`bai-46`](https://github.com/toidicode/taskflow/tree/bai-46) | [diff](https://github.com/toidicode/taskflow/compare/bai-45...bai-46) |
| 47 | [Hàng đợi job với Redis trong Go](https://toidicode.com/hang-doi-job-voi-redis-trong-go-623.html) | [`bai-47`](https://github.com/toidicode/taskflow/tree/bai-47) | [diff](https://github.com/toidicode/taskflow/compare/bai-46...bai-47) |
| 48 | [Render HTML với html/template trong Go](https://toidicode.com/render-html-voi-htmltemplate-trong-go-624.html) | [`bai-48`](https://github.com/toidicode/taskflow/tree/bai-48) | [diff](https://github.com/toidicode/taskflow/compare/bai-47...bai-48) |
| 49 | [gRPC trong Go (Phần 1)](https://toidicode.com/grpc-trong-go-phan-1-625.html) | [`bai-49`](https://github.com/toidicode/taskflow/tree/bai-49) | [diff](https://github.com/toidicode/taskflow/compare/bai-48...bai-49) |
| 50 | [gRPC trong Go (Phần 2)](https://toidicode.com/grpc-trong-go-phan-2-626.html) | [`bai-50`](https://github.com/toidicode/taskflow/tree/bai-50) | [diff](https://github.com/toidicode/taskflow/compare/bai-49...bai-50) |
| 51 | [Metrics và tracing với OpenTelemetry trong Go](https://toidicode.com/metrics-va-tracing-voi-opentelemetry-trong-go-627.html) | [`bai-51`](https://github.com/toidicode/taskflow/tree/bai-51) | [diff](https://github.com/toidicode/taskflow/compare/bai-50...bai-51) |
| 52 | [Profiling và tối ưu hiệu năng trong Go](https://toidicode.com/profiling-va-toi-uu-hieu-nang-trong-go-628.html) | [`bai-52`](https://github.com/toidicode/taskflow/tree/bai-52) | [diff](https://github.com/toidicode/taskflow/compare/bai-51...bai-52) |
| 53 | [CI/CD cho ứng dụng Go với GitHub Actions](https://toidicode.com/cicd-cho-ung-dung-go-voi-github-actions-629.html) | [`bai-53`](https://github.com/toidicode/taskflow/tree/bai-53) | [diff](https://github.com/toidicode/taskflow/compare/bai-52...bai-53) |

## License

[MIT](LICENSE)
