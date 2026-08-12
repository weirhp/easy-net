#pragma once

#include <algorithm>
#include <cwchar>
#include <iterator>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace easy_net::history {

struct Entry {
    std::wstring mode;
    std::wstring name;
    std::wstring path;
    std::wstring arguments;
    std::wstring proxy;
    std::wstring dns;
    std::wstring last_used;
    bool isolated = false;
    std::wstring udp_mode;
    bool wechat_existing = false;
};

inline std::wstring EscapeField(std::wstring_view value) {
    std::wstring escaped;
    escaped.reserve(value.size());
    for (const wchar_t character : value) {
        switch (character) {
            case L'\\':
                escaped += L"\\\\";
                break;
            case L'\t':
                escaped += L"\\t";
                break;
            case L'\r':
                escaped += L"\\r";
                break;
            case L'\n':
                escaped += L"\\n";
                break;
            default:
                escaped.push_back(character);
                break;
        }
    }
    return escaped;
}

inline std::vector<std::wstring> ParseFields(std::wstring_view line) {
    std::vector<std::wstring> fields;
    std::wstring field;
    bool escaped = false;
    for (const wchar_t character : line) {
        if (escaped) {
            switch (character) {
                case L't':
                    field.push_back(L'\t');
                    break;
                case L'r':
                    field.push_back(L'\r');
                    break;
                case L'n':
                    field.push_back(L'\n');
                    break;
                case L'\\':
                    field.push_back(L'\\');
                    break;
                default:
                    field.push_back(L'\\');
                    field.push_back(character);
                    break;
            }
            escaped = false;
        } else if (character == L'\\') {
            escaped = true;
        } else if (character == L'\t') {
            fields.push_back(std::move(field));
            field.clear();
        } else {
            field.push_back(character);
        }
    }
    if (escaped) {
        field.push_back(L'\\');
    }
    fields.push_back(std::move(field));
    return fields;
}

inline std::vector<Entry> Parse(std::wstring_view text) {
    std::vector<Entry> entries;
    std::size_t start = 0;
    while (start < text.size()) {
        std::size_t end = text.find(L'\n', start);
        if (end == std::wstring_view::npos) {
            end = text.size();
        }
        std::wstring_view line = text.substr(start, end - start);
        if (!line.empty() && line.back() == L'\r') {
            line.remove_suffix(1);
        }
        if (!line.empty()) {
            auto fields = ParseFields(line);
            if (fields.size() >= 7 && fields.size() <= 10) {
                const bool isolated = fields.size() == 8 && fields[7] == L"1";
                const bool extended_isolated = fields.size() >= 9 && fields[7] == L"1";
                std::wstring udp_mode = fields.size() >= 9 ? std::move(fields[8]) : L"";
                const bool wechat_existing = fields.size() == 10 && fields[9] == L"1";
                entries.push_back({std::move(fields[0]), std::move(fields[1]),
                                   std::move(fields[2]), std::move(fields[3]),
                                   std::move(fields[4]), std::move(fields[5]),
                                   std::move(fields[6]), isolated || extended_isolated,
                                   std::move(udp_mode), wechat_existing});
            }
        }
        start = end + 1;
    }
    return entries;
}

inline std::wstring Serialize(const std::vector<Entry>& entries) {
    std::wstring result;
    for (const auto& entry : entries) {
        const std::wstring isolated = entry.isolated ? L"1" : L"0";
        const std::wstring wechat_existing = entry.wechat_existing ? L"1" : L"0";
        const std::wstring_view fields[]{entry.mode,      entry.name, entry.path,
                                         entry.arguments, entry.proxy, entry.dns,
                                         entry.last_used, isolated,   entry.udp_mode,
                                         wechat_existing};
        for (std::size_t index = 0; index < std::size(fields); ++index) {
            if (index != 0) {
                result.push_back(L'\t');
            }
            result += EscapeField(fields[index]);
        }
        result.push_back(L'\n');
    }
    return result;
}

inline bool CaseInsensitiveEquals(const std::wstring& left, const std::wstring& right) {
    return _wcsicmp(left.c_str(), right.c_str()) == 0;
}

inline bool SameLaunch(const Entry& left, const Entry& right) {
    return CaseInsensitiveEquals(left.mode, right.mode) &&
           CaseInsensitiveEquals(left.path, right.path) && left.arguments == right.arguments &&
           CaseInsensitiveEquals(left.proxy, right.proxy) &&
           CaseInsensitiveEquals(left.dns, right.dns) && left.isolated == right.isolated &&
           CaseInsensitiveEquals(left.udp_mode, right.udp_mode) &&
           left.wechat_existing == right.wechat_existing;
}

inline std::size_t SaveEntry(std::vector<Entry>& entries, Entry entry,
                             std::size_t editing_index = static_cast<std::size_t>(-1),
                             std::size_t maximum_entries = 30) {
    if (editing_index < entries.size()) {
        entries[editing_index] = std::move(entry);
        return editing_index;
    }
    entries.insert(entries.begin(), std::move(entry));
    if (entries.size() > maximum_entries) {
        entries.resize(maximum_entries);
    }
    return 0;
}

inline void Upsert(std::vector<Entry>& entries, Entry entry, std::size_t maximum_entries = 30) {
    entries.erase(std::remove_if(entries.begin(), entries.end(),
                                 [&entry](const Entry& existing) {
                                     return SameLaunch(existing, entry);
                                 }),
                  entries.end());
    entries.insert(entries.begin(), std::move(entry));
    if (entries.size() > maximum_entries) {
        entries.resize(maximum_entries);
    }
}

}  // namespace easy_net::history
