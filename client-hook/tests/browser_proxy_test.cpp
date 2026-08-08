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

    const auto environment = easy_net::browser::NativeSocksEnvironment(L"127.0.0.1:1080");
    assert(environment.size() == 10);
    assert(environment[0].first == L"ALL_PROXY");
    assert(environment[0].second == L"socks5h://127.0.0.1:1080");
    assert(environment[1].first == L"all_proxy");
    assert(environment[2].first == L"HTTP_PROXY");
    assert(environment[3].first == L"http_proxy");
    assert(environment[4].first == L"HTTPS_PROXY");
    assert(environment[5].first == L"https_proxy");
    assert(environment[6].first == L"WS_PROXY");
    assert(environment[7].first == L"WSS_PROXY");
    for (std::size_t index = 0; index < 8; ++index) {
        assert(environment[index].second == L"socks5h://127.0.0.1:1080");
    }
    assert(environment[8].first == L"NO_PROXY");
    assert(environment[8].second == L"localhost,127.0.0.1,::1");
    assert(environment[9].first == L"no_proxy");
    assert(environment[9].second == L"localhost,127.0.0.1,::1");
    return 0;
}
