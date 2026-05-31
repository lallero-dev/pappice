package server

import "strings"

func normalizeBranding(branding Branding) Branding {
	branding.Name = strings.TrimSpace(branding.Name)
	if branding.Name == "" {
		branding.Name = defaultBrandName
	}
	branding.Subtitle = strings.TrimSpace(branding.Subtitle)
	if branding.Subtitle == "" {
		branding.Subtitle = defaultBrandSubtitle
	}
	branding.Mark = normalizeBrandMark(branding.Mark, branding.Name)
	branding.Color = strings.TrimSpace(branding.Color)
	if !isHexColor(branding.Color) {
		branding.Color = defaultBrandColor
	}
	return branding
}

func normalizeBrandMark(mark, name string) string {
	mark = strings.TrimSpace(mark)
	if mark == "" {
		for _, char := range name {
			return strings.ToUpper(string(char))
		}
		return "P"
	}
	runes := []rune(mark)
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}

func isHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}
