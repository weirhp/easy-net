#pragma once

#include <winsock2.h>
#include <ws2tcpip.h>

#include <cwchar>
#include <cwctype>
#include <string>
#include <utility>
#include <vector>

namespace easy_net::browser {

inline bool ParseLiteralSocksEndpoint(const std::wstring& value, std::wstring& host) {
    std::wstring port_text;
    if (!value.empty() && value.front() == L'[') {
        const std::size_t close = value.find(L']');
        if (close == std::wstring::npos || close + 1 >= value.size() || value[close + 1] != L':') {
            return false;
        }
        host = value.substr(1, close - 1);
        port_text = value.substr(close + 2);
    } else {
        const std::size_t colon = value.rfind(L':');
        if (colon == std::wstring::npos) {
            return false;
        }
        host = value.substr(0, colon);
        port_text = value.substr(colon + 1);
        if (host.find(L':') != std::wstring::npos) {
            return false;
        }
    }
    wchar_t* end = nullptr;
    const long port = std::wcstol(port_text.c_str(), &end, 10);
    if (port <= 0 || port > 65535 || end == port_text.c_str() || end == nullptr || *end != L'\0') {
        return false;
    }
    in_addr ipv4{};
    in6_addr ipv6{};
    return InetPtonW(AF_INET, host.c_str(), &ipv4) == 1 ||
           InetPtonW(AF_INET6, host.c_str(), &ipv6) == 1;
}

inline std::wstring ProfileKey(const std::wstring& proxy) {
    std::wstring result = proxy;
    for (wchar_t& character : result) {
        if (std::iswalnum(character) == 0 && character != L'.' && character != L'-') {
            character = L'_';
        }
    }
    return result;
}

inline std::vector<std::wstring> NativeSocksArguments(const std::wstring& proxy,
                                                      const std::wstring& proxy_host) {
    return {
        L"--proxy-server=socks5://" + proxy,
        L"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE " + proxy_host +
            L", EXCLUDE localhost, EXCLUDE 127.0.0.1, EXCLUDE ::1",
        L"--proxy-bypass-list=localhost;127.0.0.1;[::1]",
        L"--disable-quic",
    };
}

inline std::vector<std::pair<std::wstring, std::wstring>> NativeSocksEnvironment(
    const std::wstring& proxy, bool compatible_scheme = false) {
    // Some managed applications do not recognize the curl-style socks5h:// spelling. The
    // compatible mode matches Cockpit Tools; Chromium hostname routing is still controlled by
    // --proxy-server/--host-resolver-rules.
    const std::wstring proxy_url =
        (compatible_scheme ? L"socks5://" : L"socks5h://") + proxy;
    return {
        {L"ALL_PROXY", proxy_url},
        {L"all_proxy", proxy_url},
        {L"HTTP_PROXY", proxy_url},
        {L"http_proxy", proxy_url},
        {L"HTTPS_PROXY", proxy_url},
        {L"https_proxy", proxy_url},
        {L"WS_PROXY", proxy_url},
        {L"WSS_PROXY", proxy_url},
        {L"NO_PROXY", L"localhost,127.0.0.1,127.0.0.0/8,::1,::1/128"},
        {L"no_proxy", L"localhost,127.0.0.1,127.0.0.0/8,::1,::1/128"},
    };
}

}  // namespace easy_net::browser
