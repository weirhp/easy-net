#pragma once

#include <cstdint>
#include <limits>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>

#include "tun_config.h"

namespace easy_net::wechat {

enum class HealthState {
    starting,
    healthy,
    proxy_unavailable,
    restarting,
    restart_failed,
    stopped,
};

inline const char* HealthStateName(HealthState state) {
    switch (state) {
        case HealthState::starting: return "starting";
        case HealthState::healthy: return "healthy";
        case HealthState::proxy_unavailable: return "proxy-unavailable";
        case HealthState::restarting: return "restarting";
        case HealthState::restart_failed: return "restart-failed";
        case HealthState::stopped: return "stopped";
    }
    return "stopped";
}

struct RuntimeStatus {
    std::string backend;
    HealthState state = HealthState::starting;
    std::string message;
    std::string proxy;
    std::string updated_at;
    std::uint32_t engine_pid = 0;
    std::uint32_t restart_count = 0;
    std::uint64_t heartbeat_tick_ms = 0;
    bool fail_closed = false;
};

inline std::string BuildRuntimeStatus(const RuntimeStatus& status) {
    std::ostringstream json;
    json << "{\n"
         << "  \"backend\": " << tun::JsonString(status.backend) << ",\n"
         << "  \"state\": " << tun::JsonString(HealthStateName(status.state)) << ",\n"
         << "  \"message\": " << tun::JsonString(status.message) << ",\n"
         << "  \"proxy\": " << tun::JsonString(status.proxy) << ",\n"
         << "  \"engine_pid\": " << status.engine_pid << ",\n"
         << "  \"restart_count\": " << status.restart_count << ",\n"
         << "  \"heartbeat_tick_ms\": " << status.heartbeat_tick_ms << ",\n"
         << "  \"fail_closed\": " << (status.fail_closed ? "true" : "false") << ",\n"
         << "  \"updated_at\": " << tun::JsonString(status.updated_at) << "\n"
         << "}\n";
    return json.str();
}

inline bool IsHealthyStatus(std::string_view json) {
    return json.find("\"state\": \"healthy\"") != std::string_view::npos;
}

inline std::optional<std::uint64_t> UnsignedField(std::string_view json,
                                                  std::string_view name) {
    const std::string key = "\"" + std::string(name) + "\"";
    std::size_t position = json.find(key);
    if (position == std::string_view::npos) {
        return std::nullopt;
    }
    position = json.find(':', position + key.size());
    if (position == std::string_view::npos) {
        return std::nullopt;
    }
    ++position;
    while (position < json.size() && (json[position] == ' ' || json[position] == '\t')) {
        ++position;
    }
    if (position == json.size() || json[position] < '0' || json[position] > '9') {
        return std::nullopt;
    }
    std::uint64_t value = 0;
    while (position < json.size() && json[position] >= '0' && json[position] <= '9') {
        const std::uint64_t digit = static_cast<std::uint64_t>(json[position] - '0');
        if (value > (std::numeric_limits<std::uint64_t>::max() - digit) / 10) {
            return std::nullopt;
        }
        value = value * 10 + digit;
        ++position;
    }
    return value;
}

inline bool IsFreshStatus(std::string_view json, std::uint64_t current_tick_ms,
                          std::uint64_t maximum_age_ms = 30000) {
    const auto heartbeat = UnsignedField(json, "heartbeat_tick_ms");
    return heartbeat && current_tick_ms >= *heartbeat &&
           current_tick_ms - *heartbeat <= maximum_age_ms;
}

}  // namespace easy_net::wechat
