#include <cassert>
#include <string>

#include "../src/tun_config.h"

int main() {
    using easy_net::tun::Config;
    using easy_net::tun::Endpoint;
    using easy_net::tun::Stack;
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

    Stack stack{};
    assert(easy_net::tun::ParseStack("SYSTEM", stack) && stack == Stack::system);
    assert(easy_net::tun::ParseStack("mixed", stack) && stack == Stack::mixed);
    assert(!easy_net::tun::ParseStack("fast", stack));

    std::vector<std::string> exclusions;
    assert(easy_net::tun::AppendRouteExclusions(
        "1.116.229.59/32, 192.168.0.0/16", exclusions));
    assert(exclusions.size() == 2);
    assert(easy_net::tun::AppendRouteExclusions("1.116.229.59/32", exclusions));
    assert(exclusions.size() == 2);
    assert(!easy_net::tun::AppendRouteExclusions("1.116.229.999/32", exclusions));
    assert(!easy_net::tun::AppendRouteExclusions("1.116.229.59/33", exclusions));

    Config config;
    config.proxy = {"127.0.0.1", 1082};
    config.interface_name = "easy-net-wechat-1234";
    config.username = "user\"name";
    config.password = "secret";
    config.dns_host = "223.5.5.5";
    config.dns_port = 53;
    config.log_path = "C:\\Easy Net\\tun.log";
    config.udp_mode = UdpMode::block;
    config.route_exclude_addresses.push_back("1.116.229.59/32");
    const std::string json = easy_net::tun::BuildConfig(config);
    assert(json.find("\"level\": \"warn\"") != std::string::npos);
    assert(json.find("\"interface_name\": \"easy-net-wechat-1234\"") != std::string::npos);
    assert(json.find("\"username\": \"user\\\"name\"") != std::string::npos);
    assert(json.find("\"action\": \"hijack-dns\"") != std::string::npos);
    assert(json.find("\"network\": \"udp\"") != std::string::npos);
    assert(json.find("Weixin.exe") != std::string::npos);
    assert(json.find("WeChatApp.exe") != std::string::npos);
    assert(json.find("\"stack\": \"system\"") != std::string::npos);
    assert(json.find("1.116.229.59/32") != std::string::npos);
    assert(json.find("10.0.0.0/8") != std::string::npos);
    assert(json.find("fc00::/7") != std::string::npos);

    config.udp_mode = UdpMode::proxy;
    config.log_level = "info";
    config.stack = Stack::mixed;
    const std::string proxy_udp = easy_net::tun::BuildConfig(config);
    assert(proxy_udp.find("\"level\": \"info\"") != std::string::npos);
    assert(proxy_udp.find("\"network\": \"udp\"") == std::string::npos);
    assert(proxy_udp.find("\"stack\": \"mixed\"") != std::string::npos);
    return 0;
}
