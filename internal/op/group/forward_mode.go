package group

func validGroupForwardMode(mode string) bool {
	switch mode {
	case "inherit", "convert", "passthrough", "raw":
		return true
	}
	return false
}
