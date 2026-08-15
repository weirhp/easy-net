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

    entries[0].id = L"entry-123";
    const auto with_id = easy_net::history::Parse(easy_net::history::Serialize(entries));
    assert(with_id[0].id == L"entry-123");

    Entry wechat{L"wechat", L"微信", L"D:\\Weixin\\Weixin.exe", L"",
                 L"127.0.0.1:10808", L"223.5.5.5:53", L"102", false, L"proxy"};
    easy_net::history::Upsert(entries, wechat);
    const auto with_udp_mode = easy_net::history::Parse(easy_net::history::Serialize(entries));
    assert(with_udp_mode.front().mode == L"wechat");
    assert(with_udp_mode.front().udp_mode == L"proxy");

    Entry existing_wechat = wechat;
    existing_wechat.wechat_existing = true;
    const auto existing_round_trip = easy_net::history::Parse(
        easy_net::history::Serialize({existing_wechat}));
    assert(existing_round_trip.size() == 1);
    assert(existing_round_trip[0].wechat_existing);

    existing_wechat.engine_profile_id = L"engine-profile-1";
    const auto engine_round_trip = easy_net::history::Parse(
        easy_net::history::Serialize({existing_wechat}));
    assert(engine_round_trip.size() == 1);
    assert(engine_round_trip[0].engine_profile_id == L"engine-profile-1");

    const auto legacy = easy_net::history::Parse(
        L"antigravity\tAntigravity IDE\tD:\\\\IDE.exe\t\t127.0.0.1:1082\t\t98\n");
    assert(legacy.size() == 1);
    assert(!legacy[0].isolated);
    assert(legacy[0].id.empty());

    auto updated = parsed;
    Entry replacement = parsed[0];
    replacement.name = L"Renamed";
    replacement.last_used = L"101";
    easy_net::history::Upsert(updated, replacement);
    assert(updated.size() == 2);
    assert(updated[0].name == L"Renamed");
    assert(updated[0].last_used == L"101");

    Entry edited = updated[1];
    edited.name = L"Edited in place";
    const std::size_t edited_index = easy_net::history::SaveEntry(updated, edited, 1);
    assert(edited_index == 1);
    assert(updated.size() == 2);
    assert(updated[1].name == L"Edited in place");

    Entry created = updated[0];
    created.name = L"New shortcut";
    const std::size_t created_index = easy_net::history::SaveEntry(updated, created);
    assert(created_index == 0);
    assert(updated.front().name == L"New shortcut");

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
