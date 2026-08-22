#include <cassert>
#include <string>
#include <vector>

#include "../src/network_config.h"

int main() {
    using easy_net::network::Endpoint;
    using easy_net::network::UdpMode;

    Endpoint endpoint;
    assert(easy_net::network::ParseEndpoint("127.0.0.1:1082", endpoint));
    assert(endpoint.host == "127.0.0.1" && endpoint.port == 1082);
    assert(easy_net::network::ParseEndpoint("[::1]:1080", endpoint));
    assert(endpoint.host == "::1" && endpoint.port == 1080);
    assert(easy_net::network::ParseEndpoint("223.5.5.5", endpoint, 53));
    assert(!easy_net::network::ParseEndpoint("127.0.0.1:0", endpoint));

    UdpMode mode{};
    assert(easy_net::network::ParseUdpMode("AUTO", mode) && mode == UdpMode::automatic);
    assert(easy_net::network::ParseUdpMode("proxy", mode) && mode == UdpMode::proxy);
    assert(!easy_net::network::ParseUdpMode("leak", mode));

    std::vector<std::string> prefixes;
    assert(easy_net::network::AppendBypassPrefixes(
        "1.116.229.59/32, 192.168.0.0/16", prefixes));
    assert(prefixes.size() == 2);
    assert(easy_net::network::AppendBypassPrefixes("1.116.229.59/32", prefixes));
    assert(prefixes.size() == 2);
    assert(!easy_net::network::AppendBypassPrefixes("1.116.229.999/32", prefixes));
    assert(!easy_net::network::AppendBypassPrefixes("1.116.229.59/33", prefixes));
    assert(easy_net::network::JsonString("a\"b") == "\"a\\\"b\"");
    assert(easy_net::network::WeChatProcessNames().size() >= 10);
    return 0;
}
