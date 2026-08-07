#include <cassert>
#include <string>

#include "../src/config_ipc.h"

int wmain() {
    easy_net::ipc::ConfigBlock block;
    assert(easy_net::ipc::CopyString(block.proxy, L"127.0.0.1:1080"));
    assert(easy_net::ipc::CopyString(block.dns, L"223.5.5.5:53"));
    assert(easy_net::ipc::IsValid(block));
    assert(std::wstring(block.proxy) == L"127.0.0.1:1080");
    assert(easy_net::ipc::ConfigMappingName(1234) == L"Local\\EasyNetHookConfig-1234");

    const std::wstring too_long(std::size(block.proxy), L'x');
    assert(!easy_net::ipc::CopyString(block.proxy, too_long));

    block.version += 1;
    assert(!easy_net::ipc::IsValid(block));
    return 0;
}
