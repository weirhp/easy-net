#include <cassert>

#include "../src/child_injection.h"

int main() {
    assert(easy_net::child::ShouldInject(static_cast<const wchar_t*>(nullptr)));
    assert(easy_net::child::ShouldInject(L"helper.exe --background"));
    assert(easy_net::child::ShouldInject("helper.exe --background"));
    assert(easy_net::child::ShouldInject(L"helper.exe --type=worker"));

    assert(!easy_net::child::ShouldInject(L"ChatGPT.exe --type=renderer"));
    assert(!easy_net::child::ShouldInject(L"ChatGPT.exe --type=gpu-process"));
    assert(!easy_net::child::ShouldInject(L"ChatGPT.exe --type=crashpad-handler"));
    assert(!easy_net::child::ShouldInject(
        L"ChatGPT.exe --type=utility --utility-sub-type=storage.mojom.StorageService"));
    assert(!easy_net::child::ShouldInject("ChatGPT.exe --type=utility"));

    assert(easy_net::child::ShouldInject(
        L"ChatGPT.exe --type=utility --utility-sub-type=network.mojom.NetworkService"));
    assert(easy_net::child::ShouldInject(
        "ChatGPT.exe --utility-sub-type=network.mojom.NetworkService --type=utility"));
    assert(easy_net::child::ShouldInject(
        L"Cursor.exe --type=utility --utility-sub-type=node.mojom.NodeService "
        L"--service-sandbox-type=none"));
    assert(easy_net::child::ShouldInject(
        "Cursor.exe --utility-sub-type=node.mojom.NodeService --type=utility"));
    return 0;
}
