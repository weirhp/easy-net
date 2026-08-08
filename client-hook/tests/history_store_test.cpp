#include <cassert>
#include <string>
#include <vector>

#include "../src/history_store.h"

int wmain() {
    using easy_net::history::Entry;
    std::vector<Entry> entries{
        {L"hook", L"Demo", L"C:\\Apps\\demo.exe", L"--name \"a\tb\"", L"127.0.0.1:1080",
         L"", L"100"},
        {L"antigravity", L"Antigravity IDE", L"D:\\IDE\\Antigravity IDE.exe",
         L"D:\\work\nproject", L"127.0.0.1:1082", L"", L"99", true},
    };
    const std::wstring serialized = easy_net::history::Serialize(entries);
    const auto parsed = easy_net::history::Parse(serialized);
    assert(parsed.size() == 2);
    assert(parsed[0].path == entries[0].path);
    assert(parsed[0].arguments == entries[0].arguments);
    assert(parsed[1].arguments == entries[1].arguments);
    assert(parsed[1].isolated);

    Entry wechat{L"wechat", L"微信", L"D:\\Weixin\\Weixin.exe", L"",
                 L"127.0.0.1:10808", L"223.5.5.5:53", L"102", false, L"proxy"};
    easy_net::history::Upsert(entries, wechat);
    const auto with_udp_mode = easy_net::history::Parse(easy_net::history::Serialize(entries));
    assert(with_udp_mode.front().mode == L"wechat");
    assert(with_udp_mode.front().udp_mode == L"proxy");

    const auto legacy = easy_net::history::Parse(
        L"antigravity\tAntigravity IDE\tD:\\\\IDE.exe\t\t127.0.0.1:1082\t\t98\n");
    assert(legacy.size() == 1);
    assert(!legacy[0].isolated);

    auto updated = parsed;
    Entry replacement = parsed[0];
    replacement.name = L"Renamed";
    replacement.last_used = L"101";
    easy_net::history::Upsert(updated, replacement);
    assert(updated.size() == 2);
    assert(updated[0].name == L"Renamed");
    assert(updated[0].last_used == L"101");

    std::vector<Entry> limited;
    for (int index = 0; index < 5; ++index) {
        Entry item{L"hook", std::to_wstring(index), L"C:\\" + std::to_wstring(index) + L".exe",
                   L"", L"127.0.0.1:1080", L"", std::to_wstring(index)};
        easy_net::history::Upsert(limited, std::move(item), 3);
    }
    assert(limited.size() == 3);
    assert(limited.front().name == L"4");
    return 0;
}
