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
         L"D:\\work\nproject", L"127.0.0.1:1082", L"", L"99"},
    };
    const std::wstring serialized = easy_net::history::Serialize(entries);
    const auto parsed = easy_net::history::Parse(serialized);
    assert(parsed.size() == 2);
    assert(parsed[0].path == entries[0].path);
    assert(parsed[0].arguments == entries[0].arguments);
    assert(parsed[1].arguments == entries[1].arguments);

    auto updated = parsed;
    Entry replacement = entries[0];
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
