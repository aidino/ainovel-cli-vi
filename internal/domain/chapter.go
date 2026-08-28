package domain

import "fmt"

// ReviewInterval khoảng cách đọc kiểm toàn cục (kích hoạt mỗi N chương).
const ReviewInterval = 5

// ShouldReview theo số chương đã hoàn thành phán đoán có cần đọc kiểm toàn cục không (chế độ truyện ngắn / trung).
func ShouldReview(completedCount int) (bool, string) {
	if completedCount > 0 && completedCount%ReviewInterval == 0 {
		return true, fmt.Sprintf("đã hoàn thành %d chương, kích hoạt đọc kiểm toàn cục", completedCount)
	}
	return false, ""
}

// ShouldArcReview ở chế độ trường thiên, phán đoán có cần đọc kiểm cấp arc / cấp tập không.
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("tập %d arc %d kết thúc (kết thúc tập), kích hoạt đọc kiểm cấp arc + cấp tập", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("tập %d arc %d kết thúc, kích hoạt đọc kiểm cấp arc", volume, arc)
	}
	return false, ""
}
