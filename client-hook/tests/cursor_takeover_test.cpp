#include <cassert>
#include <string>

#include "../src/cursor_takeover.h"

int main() {
    const std::string profile = R"({
      "ProxyConfigs": [],
      "EasyNetCursorNodeProxy": "127.0.0.1:1082",
      "ProxyRules": []
    })";
    const auto proxy = easy_net::cursor::NodeProxyFromSharedProfile(profile);
    assert(proxy && *proxy == "127.0.0.1:1082");
    assert(!easy_net::cursor::NodeProxyFromSharedProfile("{}"));
    assert(!easy_net::cursor::NodeProxyFromSharedProfile(
        R"({"EasyNetCursorNodeProxy":"bad\nvalue"})"));

    assert(easy_net::cursor::ShouldInjectTakeoverProcess(L"Cursor.exe"));
    assert(easy_net::cursor::ShouldInjectTakeoverProcess(
        L"Cursor.exe --type=utility --utility-sub-type=node.mojom.NodeService"));
    assert(easy_net::cursor::ShouldInjectTakeoverProcess(
        L"Cursor.exe --type=utility --utility-sub-type=network.mojom.NetworkService"));
    assert(!easy_net::cursor::ShouldInjectTakeoverProcess(
        L"Cursor.exe --type=renderer"));
    assert(!easy_net::cursor::ShouldInjectTakeoverProcess(
        L"Cursor.exe --type=gpu-process"));
    return 0;
}
