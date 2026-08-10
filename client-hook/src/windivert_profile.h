#pragma once

#include <array>
#include <cstdint>
#include <sstream>
#include <string>
#include <string_view>
#include <vector>

#include "tun_config.h"

namespace easy_net::windivert {

enum class Backend {
    tun,
    windivert,
};

inline bool ParseBackend(std::string_view value, Backend& backend) {
    if (value == "tun") {
        backend = Backend::tun;
        return true;
    }
    if (value == "windivert") {
        backend = Backend::windivert;
        return true;
    }
    return false;
}

struct Profile {
    tun::Endpoint proxy;
    std::string username;
    std::string password;
    tun::UdpMode udp_mode = tun::UdpMode::block;
    bool traffic_logging = false;
    std::vector<std::string> bypass_prefixes{
        "10.0.0.0/8",
        "100.64.0.0/10",
        "169.254.0.0/16",
        "172.16.0.0/12",
        "192.168.0.0/16",
        "fc00::/7",
        "fe80::/10",
    };
};

inline std::string ProcessNames() {
    std::string output;
    for (const auto& name : tun::WeChatProcessNames()) {
        if (!output.empty()) {
            output.push_back(';');
        }
        output += name;
    }
    return output;
}

inline bool ParseIpv4(std::string_view value, std::uint32_t& address) {
    std::array<unsigned int, 4> parts{};
    std::size_t start = 0;
    for (std::size_t index = 0; index < parts.size(); ++index) {
        const std::size_t end = value.find('.', start);
        if ((index < 3 && end == std::string_view::npos) ||
            (index == 3 && end != std::string_view::npos)) {
            return false;
        }
        const std::string_view part = value.substr(start,
            (end == std::string_view::npos ? value.size() : end) - start);
        if (!tun::ParseDecimal(part, 255)) {
            return false;
        }
        unsigned int parsed = 0;
        for (const char character : part) {
            parsed = parsed * 10 + static_cast<unsigned int>(character - '0');
        }
        parts[index] = parsed;
        start = end == std::string_view::npos ? value.size() : end + 1;
    }
    address = (parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3];
    return true;
}

inline std::string FormatIpv4(std::uint32_t address) {
    return std::to_string((address >> 24) & 0xff) + "." +
           std::to_string((address >> 16) & 0xff) + "." +
           std::to_string((address >> 8) & 0xff) + "." +
           std::to_string(address & 0xff);
}

// ProxyBridge accepts IPv4 ranges, not IPv4 CIDR notation. IPv6 CIDRs can be
// passed through unchanged.
inline bool RoutePrefixToHostPattern(std::string_view prefix, std::string& pattern) {
    const std::size_t slash = prefix.find('/');
    if (slash == std::string_view::npos) {
        return false;
    }
    if (prefix.substr(0, slash).find(':') != std::string_view::npos) {
        if (!tun::IsRoutePrefix(prefix)) {
            return false;
        }
        pattern.assign(prefix);
        return true;
    }
    std::uint32_t address = 0;
    if (!ParseIpv4(prefix.substr(0, slash), address)) {
        return false;
    }
    unsigned int bits = 0;
    const auto bit_text = prefix.substr(slash + 1);
    if (!tun::ParseDecimal(bit_text, 32)) {
        return false;
    }
    for (const char character : bit_text) {
        bits = bits * 10 + static_cast<unsigned int>(character - '0');
    }
    const std::uint32_t mask = bits == 0 ? 0 : 0xffffffffu << (32 - bits);
    const std::uint32_t first = address & mask;
    const std::uint32_t last = first | ~mask;
    pattern = FormatIpv4(first);
    if (first != last) {
        pattern += "-" + FormatIpv4(last);
    }
    return true;
}

inline void AddRule(std::ostringstream& json, bool& first_rule,
                    std::string_view processes, std::string_view hosts,
                    std::string_view protocol, std::string_view action) {
    if (!first_rule) {
        json << ",\n";
    }
    first_rule = false;
    json << "    {\"ProcessName\": " << tun::JsonString(processes)
         << ", \"TargetHosts\": " << tun::JsonString(hosts)
         << ", \"TargetPorts\": \"*\", \"TargetDomains\": \"*\", "
         << "\"Protocol\": " << tun::JsonString(protocol)
         << ", \"Action\": " << tun::JsonString(action)
         << ", \"IsEnabled\": true, \"ProxyConfigId\": 1}";
}

inline std::string BuildProfile(const Profile& profile) {
    const std::string processes = ProcessNames();
    std::ostringstream json;
    json << "{\n"
         << "  \"Version\": \"1.0\",\n"
         << "  \"LocalhostViaProxy\": false,\n"
         << "  \"IsTrafficLoggingEnabled\": "
         << (profile.traffic_logging ? "true" : "false") << ",\n"
         << "  \"ProxyConfigs\": [{\"Id\": 1, \"Type\": \"socks5\", \"Host\": "
         << tun::JsonString(profile.proxy.host) << ", \"Port\": "
         << tun::JsonString(std::to_string(profile.proxy.port))
         << ", \"Username\": " << tun::JsonString(profile.username)
         << ", \"Password\": " << tun::JsonString(profile.password) << "}],\n"
         << "  \"ProxyRules\": [\n";
    bool first_rule = true;
    for (const auto& prefix : profile.bypass_prefixes) {
        std::string pattern;
        if (RoutePrefixToHostPattern(prefix, pattern)) {
            AddRule(json, first_rule, processes, pattern, "BOTH", "DIRECT");
        }
    }
    AddRule(json, first_rule, processes, "*", "TCP", "PROXY");
    switch (profile.udp_mode) {
        case tun::UdpMode::proxy:
        case tun::UdpMode::automatic:
            AddRule(json, first_rule, processes, "*", "UDP", "PROXY");
            break;
        case tun::UdpMode::block:
            AddRule(json, first_rule, processes, "*", "UDP", "BLOCK");
            break;
        case tun::UdpMode::direct:
            AddRule(json, first_rule, processes, "*", "UDP", "DIRECT");
            break;
    }
    json << "\n  ]\n}\n";
    return json.str();
}

}  // namespace easy_net::windivert
