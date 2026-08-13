#pragma once

#include <initializer_list>
#include <string_view>

namespace easy_net::child {

template <typename Character>
inline bool ShouldInjectImpl(const Character* command_line,
                             std::basic_string_view<Character> network_service_switch,
                             std::basic_string_view<Character> node_service_switch,
                             std::initializer_list<std::basic_string_view<Character>>
                                 chromium_child_switches) {
    if (command_line == nullptr) {
        return true;
    }

    const std::basic_string_view<Character> command(command_line);
    if (command.find(network_service_switch) != std::basic_string_view<Character>::npos ||
        command.find(node_service_switch) != std::basic_string_view<Character>::npos) {
        return true;
    }

    // Chromium/Electron isolates rendering, GPU, storage, crash reporting, and
    // networking in different child processes. Injecting every sandboxed child
    // can prevent the renderer from starting. The network service and unsandboxed
    // Node service may both initiate external connections (Cursor uses the latter
    // for AI and extension-host traffic), so those two receive the Winsock hook.
    // Ordinary non-Chromium children retain recursive injection behavior.
    for (const auto child_switch : chromium_child_switches) {
        if (command.find(child_switch) != std::basic_string_view<Character>::npos) {
            return false;
        }
    }
    return true;
}

inline bool ShouldInject(const wchar_t* command_line) {
    return ShouldInjectImpl(
        command_line, std::wstring_view(L"--utility-sub-type=network.mojom.NetworkService"),
        std::wstring_view(L"--utility-sub-type=node.mojom.NodeService"),
        {std::wstring_view(L"--type=renderer"), std::wstring_view(L"--type=gpu-process"),
         std::wstring_view(L"--type=crashpad-handler"), std::wstring_view(L"--type=utility"),
         std::wstring_view(L"--type=broker"), std::wstring_view(L"--type=zygote"),
         std::wstring_view(L"--type=plugin"), std::wstring_view(L"--type=ppapi")});
}

inline bool ShouldInject(const char* command_line) {
    return ShouldInjectImpl(
        command_line, std::string_view("--utility-sub-type=network.mojom.NetworkService"),
        std::string_view("--utility-sub-type=node.mojom.NodeService"),
        {std::string_view("--type=renderer"), std::string_view("--type=gpu-process"),
         std::string_view("--type=crashpad-handler"), std::string_view("--type=utility"),
         std::string_view("--type=broker"), std::string_view("--type=zygote"),
         std::string_view("--type=plugin"), std::string_view("--type=ppapi")});
}

}  // namespace easy_net::child
