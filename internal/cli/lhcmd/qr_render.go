package lhcmd

import "github.com/skip2/go-qrcode"

func renderQR(text string) string {
	code, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return text
	}
	return code.ToSmallString(false)
}
