#include <cassert>
#include <string>

#include "../src/tun_config.h"

int main() {
    using easy_net::tun::Config;
    using easy_net::tun::Endpoint;
    using easy_net::tun::UdpMode;

    Endpoint endpoint;
    assert(easy_net::tun::ParseEndpoint("127.0.0.1:1082", endpoint));
    assert(endpoint.host == "127.0.0.1");
    assert(endpoint.port == 1082);
    assert(easy_net::tun::ParseEndpoint("[::1]:1080", endpoint));
    assert(endpoint.host == "::1");
    assert(endpoint.port == 1080);
    assert(easy_net::tun::ParseEndpoint("223.5.5.5", endpoint, 53));
    assert(endpoint.port == 53);
    assert(!easy_net::tun::ParseEndpoint("127.0.0.1:0", endpoint));

    UdpMode mode{};
    assert(easy_net::tun::ParseUdpMode("AUTO", mode) && mode == UdpMode::automatic);
    assert(easy_net::tun::ParseUdpMode("proxy", mode) && mode == UdpMode::proxy);
    assert(!easy_net::tun::ParseUdpMode("leak", mode));

    Config config;
    config.proxy = {"127.0.0.1", 1082};
    config.username = "user\"name";
    config.password = "secret";
    config.dns_host = "223.5.5.5";
    config.dns_port = 53;
    config.log_path = "C:\\Easy Net\\tun.log";
    config.udp_mode = UdpMode::block;
    const std::string json = easy_net::tun::BuildConfig(config);
    assert(json.find("\\\"username\\\": \\\"user\\\\\\\"name\\\"") != std::string::npos);
    assert(json.find("\\\"action\\\": \\\"hijack-dns\\\"") != std::string::npos);
    assert(json.find("\\\"network\\\": \\\"udp\\\"") != std::string::npos);
    assert(json.find("Weixin.exe") != std::string::npos);

    config.udp_mode = UdpMode::proxy;
    const std::string proxy_udp = easy_net::tun::BuildConfig(config);
    assert(proxy_udp.find("\\\"network\\\": \\\"udp\\\"") == std::string::npos);
    return 0;
}
