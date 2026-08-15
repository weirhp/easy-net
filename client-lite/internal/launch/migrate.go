package launch

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"easy-net/client-lite/internal/model"
	"easy-net/client-lite/internal/service"
)

func defaultHistoryPath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		return ""
	}
	return filepath.Join(base, "EasyNetHook", "launcher-entries.tsv")
}

func migrateFromHistory(proxies *service.Service, historyPath string) ([]model.LaunchEntry, error) {
	if historyPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取旧启动入口：%w", err)
	}
	return parseHistoryTSV(string(decodeTSV(data)), proxies), nil
}

func decodeTSV(data []byte) []byte {
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		count := (len(data) - 2) / 2
		u16 := make([]uint16, count)
		for i := 0; i < count; i++ {
			u16[i] = binary.LittleEndian.Uint16(data[2+i*2:])
		}
		return []byte(string(utf16.Decode(u16)))
	}
	return data
}

func parseHistoryTSV(text string, proxies *service.Service) []model.LaunchEntry {
	listenByAddress := map[string]string{}
	if proxies != nil {
		for _, state := range proxies.States() {
			listenByAddress[normalizeListen(state.Profile.ListenAddress())] = state.Profile.ID
			listenByAddress[normalizeListen(fmt.Sprintf("127.0.0.1:%d", state.Profile.ListenPort))] = state.Profile.ID
		}
	}
	seen := map[string]struct{}{}
	var entries []model.LaunchEntry
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := parseTSVFields(line)
		if len(fields) < 7 || len(fields) > 12 {
			continue
		}
		entry := model.LaunchEntry{
			Mode:      model.LaunchMode(strings.TrimSpace(fields[0])),
			Name:      strings.TrimSpace(fields[1]),
			Path:      strings.TrimSpace(fields[2]),
			Arguments: strings.TrimSpace(fields[3]),
			DNS:       strings.TrimSpace(fields[5]),
		}
		if len(fields) >= 8 {
			entry.Isolated = fields[7] == "1"
		}
		if len(fields) >= 9 {
			entry.UDPMode = strings.TrimSpace(fields[8])
		}
		if len(fields) >= 10 {
			entry.WeChatExisting = fields[9] == "1"
		}
		if len(fields) >= 11 {
			entry.ID = strings.TrimSpace(fields[10])
		}
		proxy := strings.TrimSpace(fields[4])
		if profileID, ok := listenByAddress[normalizeListen(proxy)]; ok {
			entry.ProfileID = profileID
		}
		if entry.ID == "" {
			entry.ID = newID()
		}
		if _, exists := seen[entry.ID]; exists {
			entry.ID = newID()
		}
		entry.Normalize()
		if err := entry.Validate(); err != nil {
			continue
		}
		seen[entry.ID] = struct{}{}
		entries = append(entries, entry)
		if len(entries) >= model.MaxLaunchEntries {
			break
		}
	}
	return entries
}

func parseTSVFields(line string) []string {
	var fields []string
	var field strings.Builder
	escaped := false
	for _, character := range line {
		if escaped {
			switch character {
			case 't':
				field.WriteByte('\t')
			case 'r':
				field.WriteByte('\r')
			case 'n':
				field.WriteByte('\n')
			case '\\':
				field.WriteByte('\\')
			default:
				field.WriteByte('\\')
				field.WriteRune(character)
			}
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '\t' {
			fields = append(fields, field.String())
			field.Reset()
			continue
		}
		field.WriteRune(character)
	}
	if escaped {
		field.WriteByte('\\')
	}
	fields = append(fields, field.String())
	return fields
}

func normalizeListen(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	host, port, err := splitHostPortLoose(value)
	if err != nil {
		return value
	}
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	return host + ":" + port
}

func splitHostPortLoose(value string) (string, string, error) {
	if i := strings.LastIndex(value, ":"); i >= 0 {
		return strings.Trim(value[:i], "[]"), value[i+1:], nil
	}
	return "", "", fmt.Errorf("missing port")
}
