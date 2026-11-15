// Package auth băm mật khẩu bằng bcrypt, phát và kiểm tra access token (JWT) của TaskFlow.
package auth

import "golang.org/x/crypto/bcrypt"

const (
	// bcryptCost: mỗi lần tăng 1 thì thời gian băm gấp đôi, 12 mất khoảng 0,3 giây.
	bcryptCost = 12
	// MaxPasswordBytes là giới hạn của bcrypt: mật khẩu dài hơn 72 byte bị từ chối.
	MaxPasswordBytes = 72
)

// HashPassword băm password bằng bcrypt. Kết quả đã chứa thuật toán, cost và salt ngẫu nhiên,
// lưu nguyên chuỗi này vào database. Lỗi bcrypt.ErrPasswordTooLong khi password quá 72 byte.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

// CheckPassword cho biết password có khớp với hash không. hash rỗng (không có user với email
// đó) thì vẫn so với một hash giả cùng cost rồi trả false: thời gian trả lời gần như nhau,
// kẻ tấn công không đo được email nào đã đăng ký.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash là hash của một mật khẩu không thuộc về ai. Tính sẵn lúc khởi động (mất khoảng
// 0,3 giây) để cả lần đăng nhập đầu tiên với email lạ cũng không chậm hơn bình thường.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("taskflow-khong-phai-mat-khau-that"), bcryptCost)
