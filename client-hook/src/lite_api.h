#pragma once

#include <windows.h>
#include <winhttp.h>

#include <cstdint>
#include <cwchar>
#include <filesystem>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace easy_net::lite_api {

inline std::size_t SkipJsonSpace(std::string_view json, std::size_t index) {
    while (index < json.size() &&
           (json[index] == ' ' || json[index] == '\t' || json[index] == '\r' ||
            json[index] == '\n')) {
        ++index;
    }
    return index;
}

inline int JsonHexDigit(char value) {
    if (value >= '0' && value <= '9') return value - '0';
    if (value >= 'a' && value <= 'f') return value - 'a' + 10;
    if (value >= 'A' && value <= 'F') return value - 'A' + 10;
    return -1;
}

inline std::optional<std::uint32_t> DecodeJsonHex4(std::string_view json,
                                                   std::size_t& index) {
    if (index + 4 > json.size()) return std::nullopt;
    std::uint32_t value = 0;
    for (int count = 0; count < 4; ++count) {
        const int digit = JsonHexDigit(json[index++]);
        if (digit < 0) return std::nullopt;
        value = (value << 4) | static_cast<std::uint32_t>(digit);
    }
    return value;
}

inline void AppendUtf8(std::string& value, std::uint32_t code_point) {
    if (code_point <= 0x7f) {
        value.push_back(static_cast<char>(code_point));
    } else if (code_point <= 0x7ff) {
        value.push_back(static_cast<char>(0xc0 | (code_point >> 6)));
        value.push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
    } else if (code_point <= 0xffff) {
        value.push_back(static_cast<char>(0xe0 | (code_point >> 12)));
        value.push_back(static_cast<char>(0x80 | ((code_point >> 6) & 0x3f)));
        value.push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
    } else {
        value.push_back(static_cast<char>(0xf0 | (code_point >> 18)));
        value.push_back(static_cast<char>(0x80 | ((code_point >> 12) & 0x3f)));
        value.push_back(static_cast<char>(0x80 | ((code_point >> 6) & 0x3f)));
        value.push_back(static_cast<char>(0x80 | (code_point & 0x3f)));
    }
}

inline std::optional<std::string> DecodeJsonString(std::string_view json, std::size_t& index) {
    if (index >= json.size() || json[index] != '"') {
        return std::nullopt;
    }
    ++index;
    std::string value;
    while (index < json.size()) {
        const char character = json[index++];
        if (character == '"') {
            return value;
        }
        if (character != '\\') {
            value.push_back(character);
            continue;
        }
        if (index >= json.size()) {
            return std::nullopt;
        }
        const char escaped = json[index++];
        switch (escaped) {
            case '"':
            case '\\':
            case '/':
                value.push_back(escaped);
                break;
            case 'b':
                value.push_back('\b');
                break;
            case 'f':
                value.push_back('\f');
                break;
            case 'n':
                value.push_back('\n');
                break;
            case 'r':
                value.push_back('\r');
                break;
            case 't':
                value.push_back('\t');
                break;
            case 'u':
                {
                    auto code_point = DecodeJsonHex4(json, index);
                    if (!code_point) return std::nullopt;
                    if (*code_point >= 0xd800 && *code_point <= 0xdbff) {
                        if (index + 2 > json.size() || json[index] != '\\' ||
                            json[index + 1] != 'u') {
                            return std::nullopt;
                        }
                        index += 2;
                        const auto low = DecodeJsonHex4(json, index);
                        if (!low || *low < 0xdc00 || *low > 0xdfff) {
                            return std::nullopt;
                        }
                        code_point = 0x10000 + ((*code_point - 0xd800) << 10) +
                                     (*low - 0xdc00);
                    } else if (*code_point >= 0xdc00 && *code_point <= 0xdfff) {
                        return std::nullopt;
                    }
                    AppendUtf8(value, *code_point);
                    break;
                }
            default:
                return std::nullopt;
        }
    }
    return std::nullopt;
}

inline bool SkipJsonValue(std::string_view json, std::size_t& index) {
    index = SkipJsonSpace(json, index);
    if (index >= json.size()) return false;
    if (json[index] == '"') {
        return DecodeJsonString(json, index).has_value();
    }
    if (json[index] == '{' || json[index] == '[') {
        std::vector<char> closing;
        closing.push_back(json[index++] == '{' ? '}' : ']');
        while (index < json.size() && !closing.empty()) {
            if (json[index] == '"') {
                if (!DecodeJsonString(json, index)) return false;
                continue;
            }
            if (json[index] == '{') {
                closing.push_back('}');
            } else if (json[index] == '[') {
                closing.push_back(']');
            } else if (json[index] == closing.back()) {
                closing.pop_back();
            }
            ++index;
        }
        return closing.empty();
    }
    const std::size_t start = index;
    while (index < json.size() && json[index] != ',' && json[index] != '}' &&
           json[index] != ']' && json[index] != ' ' && json[index] != '\t' &&
           json[index] != '\r' && json[index] != '\n') {
        ++index;
    }
    return index > start;
}

inline std::optional<std::string> JsonRaw(std::string_view json, std::string_view key) {
    std::size_t index = SkipJsonSpace(json, 0);
    if (index >= json.size() || json[index++] != '{') return std::nullopt;
    while (index < json.size()) {
        index = SkipJsonSpace(json, index);
        if (index < json.size() && json[index] == '}') return std::nullopt;
        const auto property = DecodeJsonString(json, index);
        if (!property) return std::nullopt;
        index = SkipJsonSpace(json, index);
        if (index >= json.size() || json[index++] != ':') return std::nullopt;
        index = SkipJsonSpace(json, index);
        const std::size_t value_start = index;
        std::optional<std::string> value;
        if (index < json.size() && json[index] == '"') {
            value = DecodeJsonString(json, index);
            if (!value) return std::nullopt;
        } else {
            if (!SkipJsonValue(json, index)) return std::nullopt;
            const std::size_t value_end = index;
            if (value_start < json.size() && json[value_start] != '{' &&
                json[value_start] != '[') {
                value = std::string(json.substr(value_start, value_end - value_start));
            }
        }
        if (*property == key) return value;
        index = SkipJsonSpace(json, index);
        if (index >= json.size() || json[index] == '}') return std::nullopt;
        if (json[index++] != ',') return std::nullopt;
    }
    return std::nullopt;
}

inline std::optional<std::string> JsonString(std::string_view json, std::string_view key) {
    return JsonRaw(json, key);
}

inline std::optional<int> JsonInt(std::string_view json, std::string_view key) {
    const auto raw = JsonRaw(json, key);
    if (!raw || raw->empty()) {
        return std::nullopt;
    }
    int value = 0;
    for (const char character : *raw) {
        if (character < '0' || character > '9') {
            return std::nullopt;
        }
        value = value * 10 + (character - '0');
    }
    return value;
}

inline std::optional<bool> JsonBool(std::string_view json, std::string_view key) {
    const auto raw = JsonRaw(json, key);
    if (!raw) {
        return std::nullopt;
    }
    if (*raw == "true") {
        return true;
    }
    if (*raw == "false") {
        return false;
    }
    return std::nullopt;
}

inline std::string JsonEscape(std::string_view value) {
    std::string escaped;
    escaped.reserve(value.size() + 8);
    for (const unsigned char character : value) {
        switch (character) {
            case '"':
                escaped += "\\\"";
                break;
            case '\\':
                escaped += "\\\\";
                break;
            case '\b':
                escaped += "\\b";
                break;
            case '\f':
                escaped += "\\f";
                break;
            case '\n':
                escaped += "\\n";
                break;
            case '\r':
                escaped += "\\r";
                break;
            case '\t':
                escaped += "\\t";
                break;
            default:
                if (character < 0x20) {
                    const char hex[] = "0123456789abcdef";
                    escaped += "\\u00";
                    escaped.push_back(hex[character >> 4]);
                    escaped.push_back(hex[character & 0x0f]);
                } else {
                    escaped.push_back(static_cast<char>(character));
                }
                break;
        }
    }
    return escaped;
}

inline std::optional<std::string> WideToUtf8(std::wstring_view value) {
    if (value.empty()) {
        return std::string{};
    }
    const int size = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                                         static_cast<int>(value.size()), nullptr, 0, nullptr,
                                         nullptr);
    if (size <= 0) {
        return std::nullopt;
    }
    std::string result(static_cast<std::size_t>(size), '\0');
    if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                            static_cast<int>(value.size()), result.data(), size, nullptr,
                            nullptr) != size) {
        return std::nullopt;
    }
    return result;
}

inline std::wstring Utf8ToWide(std::string_view value) {
    if (value.empty()) {
        return {};
    }
    const int size = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                                         static_cast<int>(value.size()), nullptr, 0);
    if (size <= 0) {
        return {};
    }
    std::wstring result(static_cast<std::size_t>(size), L'\0');
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                        static_cast<int>(value.size()), result.data(), size);
    return result;
}

struct HttpResult {
    int status = 0;
    std::string body;
    std::wstring error;
};

inline bool ParseHttpUrl(const std::wstring& url, std::wstring& host, INTERNET_PORT& port,
                         std::wstring& path) {
    constexpr wchar_t prefix[] = L"http://";
    if (url.size() < 8 || url.compare(0, 7, prefix) != 0) {
        return false;
    }
    const std::wstring rest = url.substr(7);
    const std::size_t slash = rest.find(L'/');
    const std::wstring hostport = slash == std::wstring::npos ? rest : rest.substr(0, slash);
    path = slash == std::wstring::npos ? L"/" : rest.substr(slash);
    const std::size_t colon = hostport.rfind(L':');
    port = 80;
    if (colon != std::wstring::npos) {
        host = hostport.substr(0, colon);
        wchar_t* end = nullptr;
        const unsigned long parsed = std::wcstoul(hostport.c_str() + colon + 1, &end, 10);
        if (parsed == 0 || parsed > 65535 || end == nullptr || *end != L'\0') {
            return false;
        }
        port = static_cast<INTERNET_PORT>(parsed);
    } else {
        host = hostport;
    }
    return !host.empty();
}

inline HttpResult HttpRequest(const std::wstring& method, const std::wstring& url,
                              const std::string& body, const std::wstring& token) {
    HttpResult result;
    std::wstring host;
    std::wstring path;
    INTERNET_PORT port = 80;
    if (!ParseHttpUrl(url, host, port, path)) {
        result.error = L"内置代理控制地址无效。";
        return result;
    }
    HINTERNET session = WinHttpOpen(L"Easy-Net Hook", WINHTTP_ACCESS_TYPE_NO_PROXY,
                                    WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (session == nullptr) {
        result.error = L"无法初始化 HTTP 客户端。";
        return result;
    }
    WinHttpSetTimeouts(session, 3000, 3000, 8000, 8000);
    HINTERNET connect = WinHttpConnect(session, host.c_str(), port, 0);
    if (connect == nullptr) {
        WinHttpCloseHandle(session);
        result.error = L"无法连接内置代理引擎。";
        return result;
    }
    HINTERNET request = WinHttpOpenRequest(connect, method.c_str(), path.c_str(), nullptr,
                                           WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, 0);
    if (request == nullptr) {
        WinHttpCloseHandle(connect);
        WinHttpCloseHandle(session);
        result.error = L"无法创建内置代理请求。";
        return result;
    }
    std::wstring headers;
    if (!token.empty()) {
        headers += L"X-Easy-Net-Token: " + token + L"\r\n";
    }
    if (!body.empty()) {
        headers += L"Content-Type: application/json\r\n";
    }
    const BOOL sent = WinHttpSendRequest(
        request, headers.empty() ? WINHTTP_NO_ADDITIONAL_HEADERS : headers.c_str(),
        headers.empty() ? 0 : static_cast<DWORD>(-1L),
        body.empty() ? WINHTTP_NO_REQUEST_DATA : const_cast<char*>(body.data()),
        static_cast<DWORD>(body.size()), static_cast<DWORD>(body.size()), 0);
    if (!sent || !WinHttpReceiveResponse(request, nullptr)) {
        WinHttpCloseHandle(request);
        WinHttpCloseHandle(connect);
        WinHttpCloseHandle(session);
        result.error = L"内置代理引擎没有响应。";
        return result;
    }
    DWORD status = 0;
    DWORD status_size = sizeof(status);
    WinHttpQueryHeaders(request, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
                        WINHTTP_HEADER_NAME_BY_INDEX, &status, &status_size,
                        WINHTTP_NO_HEADER_INDEX);
    result.status = static_cast<int>(status);
    std::string collected;
    while (true) {
        DWORD available = 0;
        if (!WinHttpQueryDataAvailable(request, &available) || available == 0) {
            break;
        }
        if (collected.size() + available > 256 * 1024) {
            break;
        }
        const std::size_t offset = collected.size();
        collected.resize(offset + available);
        DWORD read = 0;
        if (!WinHttpReadData(request, collected.data() + offset, available, &read)) {
            collected.resize(offset);
            break;
        }
        collected.resize(offset + read);
        if (read == 0) {
            break;
        }
    }
    result.body = std::move(collected);
    WinHttpCloseHandle(request);
    WinHttpCloseHandle(connect);
    WinHttpCloseHandle(session);
    return result;
}

}  // namespace easy_net::lite_api
