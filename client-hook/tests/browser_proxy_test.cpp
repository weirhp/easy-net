#include <cassert>
#include <string>

#include "../src/browser_proxy.h"

int wmain() {
    std::wstring host;
    assert(easy_net::browser::ParseLiteralSocksEndpoint(L"127.0.0.1:1080", host));
    assert(host == L"127.0.0.1");
    assert(easy_net::browser::ParseLiteralSocksEndpoint(L"[::1]:1080", host));
    assert(host == L"::1");
    assert(!easy_net::browser::ParseLiteralSocksEndpoint(L"localhost:1080", host));
    assert(!easy_net::browser::ParseLiteralSocksEndpoint(L"::1:1080", host));
    assert(!easy_net::browser::ParseLiteralSocksEndpoint(L"127.0.0.1:0", host));
    assert(!easy_net::browser::ParseLiteralSocksEndpoint(L"127.0.0.1:65536", host));
    assert(!easy_net::browser::ParseLiteralSocksEndpoint(L"127.0.0.1", host));
    assert(easy_net::browser::ProfileKey(L"[::1]:1080") == L"___1__1080");

    const auto arguments = easy_net::browser::NativeSocksArguments(L"127.0.0.1:1080", L"127.0.0.1");
    assert(arguments.size() == 3);
    assert(arguments[0] == L"--proxy-server=socks5://127.0.0.1:1080");
    assert(arguments[1] == L"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1");
    assert(arguments[2] == L"--disable-quic");
    return 0;
}
