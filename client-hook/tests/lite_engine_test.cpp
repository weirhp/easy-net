#include <cassert>
#include <string>

#include "../src/lite_engine.h"

int wmain() {
    using easy_net::lite_engine::JsonBool;
    using easy_net::lite_engine::JsonEscape;
    using easy_net::lite_engine::JsonInt;
    using easy_net::lite_engine::JsonString;
    using easy_net::lite_engine::LooksLikeShareCode;
    using easy_net::lite_engine::ParseProxyInfo;

    const std::string json =
        "{\"ok\":true,\"id\":\"abc\",\"name\":\"测试\",\"listenAddress\":\"127.0.0.1:1082\","
        "\"listenPort\":1082,\"running\":true,\"error\":\"\"}";
    assert(JsonString(json, "id") == "abc");
    assert(JsonString(json, "listenAddress") == "127.0.0.1:1082");
    assert(JsonString("{\"error\":\"fake \\\"id\\\" value\",\"id\":\"real\"}", "id") ==
           "real");
    assert(JsonString("{\"name\":\"\\u6d4b\\u8bd5 \\ud83d\\ude80\"}", "name") ==
           "测试 🚀");
    assert(JsonInt(json, "listenPort") == 1082);
    assert(JsonBool(json, "running") == true);
    assert(JsonEscape("ENL1.a\"b\\c") == "ENL1.a\\\"b\\\\c");
    assert(LooksLikeShareCode(L"  ENL1.abc"));
    assert(!LooksLikeShareCode(L"127.0.0.1:1082"));

    easy_net::lite_engine::HttpResult response;
    response.status = 200;
    response.body = json;
    const auto info = ParseProxyInfo(response);
    assert(info.ok);
    assert(info.running);
    assert(info.id == L"abc");
    assert(info.listen_address == L"127.0.0.1:1082");
    return 0;
}
