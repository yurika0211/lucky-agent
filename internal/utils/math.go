package utils

/*
MinInt 返回两个整数中较小的那个值。
*/
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

/*
MaxInt 返回两个整数中较大的那个值。
*/
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
