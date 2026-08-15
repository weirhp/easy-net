package launch

import (
	"strings"
	"unicode"
)

func safeShortcutName(name string) string {
	name = strings.TrimSpace(name)
	var builder strings.Builder
	for _, character := range name {
		switch character {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			builder.WriteRune('_')
		default:
			if unicode.IsControl(character) {
				continue
			}
			builder.WriteRune(character)
		}
	}
	return strings.TrimRight(strings.TrimSpace(builder.String()), ".")
}
