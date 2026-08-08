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
        L"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE " + proxy_host,
        L"--disable-quic",
    };
}

inline std::vector<std::pair<std::wstring, std::wstring>> NativeSocksEnvironment(
    const std::wstring& proxy) {
    const std::wstring proxy_url = L"socks5h://" + proxy;
    return {
        {L"ALL_PROXY", proxy_url},
        {L"HTTP_PROXY", proxy_url},
        {L"HTTPS_PROXY", proxy_url},
        {L"WS_PROXY", proxy_url},
        {L"WSS_PROXY", proxy_url},
        {L"NO_PROXY", L"localhost,127.0.0.1,::1"},
    };
}

}  // namespace easy_net::browser
