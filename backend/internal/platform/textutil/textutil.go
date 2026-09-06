// Package textutil 文本脱敏小工具。
package textutil

// MaskPhone 把 11 位手机号中间四位打码，其余原样返回。
func MaskPhone(phone string) string {
	if len(phone) != 11 {
		return phone
	}
	return phone[:3] + "****" + phone[7:]
}
