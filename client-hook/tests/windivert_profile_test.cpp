#include <cassert>
#include <string>

#include "../src/windivert_profile.h"

int main() {
    using easy_net::network::UdpMode;
    using easy_net::windivert::Profile;

    std::string pattern;
    assert(easy_net::windivert::RoutePrefixToHostPattern("192.168.0.0/16", pattern));
    assert(pattern == "192.168.0.0-192.168.255.255");
    assert(easy_net::windivert::RoutePrefixToHostPattern("1.116.229.59/32", pattern));
    assert(pattern == "1.116.229.59");
    assert(easy_net::windivert::RoutePrefixToHostPattern("2001:db8::/32", pattern));
    assert(pattern == "2001:db8::/32");

    Profile profile;
    profile.proxy = {"127.0.0.1", 1082};
    profile.username = "user";
    profile.password = "p\"w";
    profile.udp_mode = UdpMode::proxy;
    profile.bypass_prefixes.push_back("1.116.229.59/32");
    const std::string json = easy_net::windivert::BuildProfile(profile);
    assert(json.find("\"Type\": \"socks5\"") != std::string::npos);
    assert(json.find("WeChatApp.exe") != std::string::npos);
    assert(json.find("1.116.229.59") != std::string::npos);
    assert(json.find("\"Protocol\": \"TCP\"") != std::string::npos);
    assert(json.find("\"Protocol\": \"UDP\"") != std::string::npos);
    assert(json.find("\"Action\": \"PROXY\"") != std::string::npos);

    profile.udp_mode = UdpMode::block;
    assert(easy_net::windivert::BuildProfile(profile).find("\"Action\": \"BLOCK\"") !=
           std::string::npos);

    assert(easy_net::windivert::ParseProcessNames("app.exe; helper.exe", profile.process_names));
    const std::string app_json = easy_net::windivert::BuildProfile(profile);
    assert(app_json.find("app.exe;helper.exe") != std::string::npos);
    assert(app_json.find("WeChatApp.exe") == std::string::npos);
    assert(!easy_net::windivert::ParseProcessNames("C:\\bad\\app.exe", profile.process_names));
    return 0;
}
