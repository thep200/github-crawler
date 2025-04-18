package model

// TruncateString cắt chuỗi xuống độ dài tối đa cho phép
// nếu chuỗi dài hơn giới hạn
func TruncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength]
}
