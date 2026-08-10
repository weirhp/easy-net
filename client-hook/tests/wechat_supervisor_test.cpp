#include <cassert>
#include <string>

#include "../src/wechat_supervisor.h"

int main() {
    easy_net::wechat::RuntimeStatus status;
    status.backend = "windivert";
    status.state = easy_net::wechat::HealthState::healthy;
    status.message = "SOCKS5 is responsive";
    status.proxy = "127.0.0.1:1082";
    status.engine_pid = 1234;
    status.restart_count = 2;
    status.heartbeat_tick_ms = 123456;
    status.fail_closed = true;
    status.updated_at = "2026-08-11T01:02:03Z";
    const std::string json = easy_net::wechat::BuildRuntimeStatus(status);
    assert(json.find("\"backend\": \"windivert\"") != std::string::npos);
    assert(json.find("\"engine_pid\": 1234") != std::string::npos);
    assert(json.find("\"restart_count\": 2") != std::string::npos);
    assert(json.find("\"heartbeat_tick_ms\": 123456") != std::string::npos);
    assert(json.find("\"fail_closed\": true") != std::string::npos);
    assert(easy_net::wechat::IsHealthyStatus(json));
    assert(easy_net::wechat::IsFreshStatus(json, 133456));
    assert(!easy_net::wechat::IsFreshStatus(json, 200000));
    assert(easy_net::wechat::UnsignedField(json, "engine_pid") == 1234);

    status.state = easy_net::wechat::HealthState::proxy_unavailable;
    assert(!easy_net::wechat::IsHealthyStatus(
        easy_net::wechat::BuildRuntimeStatus(status)));
    return 0;
}
