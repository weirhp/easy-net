#pragma once

#include <algorithm>
#include <array>
#include <cstdint>
#include <sstream>
#include <string>
#include <string_view>
#include <vector>

#include "network_config.h"

namespace easy_net::windivert {

struct Profile {
    network::Endpoint proxy;
    std::string username;
    std::string password;
    network::UdpMode udp_mode = network::UdpMode::block;
    bool traffic_logging = false;
    std::vector<std::string> process_names;
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

inline std::string ProcessNames(const Profile& profile) {
    std::string output;
    const auto& names = profile.process_names.empty() ? network::WeChatProcessNames()
                                                      : profile.process_names;
    for (const auto& name : names) {
        if (!output.empty()) {
            output.push_back(';');
        }
        output += name;
    }
    return output;
}

inline bool ParseProcessNames(std::string_view value, std::vector<std::string>& names) {
    names.clear();
    std::size_t start = 0;
    while (start <= value.size()) {
        const std::size_t separator = value.find_first_of(",;", start);
        const std::size_t end = separator == std::string_view::npos ? value.size() : separator;
        std::string_view item = value.substr(start, end - start);
        while (!item.empty() && (item.front() == ' ' || item.front() == '\t')) {
            item.remove_prefix(1);
        }
        while (!item.empty() && (item.back() == ' ' || item.back() == '\t')) {
            item.remove_suffix(1);
        }
        if (item.empty() || item.size() > 260 || item.find_first_of("\\/:*?\"<>|") != std::string_view::npos) {
            names.clear();
            return false;
        }
        const std::string name(item);
        if (std::find(names.begin(), names.end(), name) == names.end()) {
            names.push_back(name);
        }
        if (separator == std::string_view::npos) {
            break;
        }
        start = separator + 1;
    }
    return !names.empty();
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
        if (!network::ParseDecimal(part, 255)) {
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
        if (!network::IsRoutePrefix(prefix)) {
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
    if (!network::ParseDecimal(bit_text, 32)) {
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
    json << "    {\"ProcessName\": " << network::JsonString(processes)
         << ", \"TargetHosts\": " << network::JsonString(hosts)
         << ", \"TargetPorts\": \"*\", \"TargetDomains\": \"*\", "
         << "\"Protocol\": " << network::JsonString(protocol)
         << ", \"Action\": " << network::JsonString(action)
         << ", \"IsEnabled\": true, \"ProxyConfigId\": 1}";
}

inline std::string BuildProfile(const Profile& profile) {
    const std::string processes = ProcessNames(profile);
    std::ostringstream json;
    json << "{\n"
         << "  \"Version\": \"1.0\",\n"
         << "  \"LocalhostViaProxy\": false,\n"
         << "  \"IsTrafficLoggingEnabled\": "
         << (profile.traffic_logging ? "true" : "false") << ",\n"
         << "  \"ProxyConfigs\": [{\"Id\": 1, \"Type\": \"socks5\", \"Host\": "
         << network::JsonString(profile.proxy.host) << ", \"Port\": "
         << network::JsonString(std::to_string(profile.proxy.port))
         << ", \"Username\": " << network::JsonString(profile.username)
         << ", \"Password\": " << network::JsonString(profile.password) << "}],\n"
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
        case network::UdpMode::proxy:
        case network::UdpMode::automatic:
            AddRule(json, first_rule, processes, "*", "UDP", "PROXY");
            break;
        case network::UdpMode::block:
            AddRule(json, first_rule, processes, "*", "UDP", "BLOCK");
            break;
        case network::UdpMode::direct:
            AddRule(json, first_rule, processes, "*", "UDP", "DIRECT");
            break;
    }
    json << "\n  ]\n}\n";
    return json.str();
}

}  // namespace easy_net::windivert
