cmake_minimum_required(VERSION 3.24)

project(easy_net_hook LANGUAGES C CXX RC)

if(NOT WIN32 OR NOT MSVC)
    message(FATAL_ERROR "Easy-Net Hook currently requires Windows and MSVC.")
endif()

include(FetchContent)

FetchContent_Declare(
    detours
    GIT_REPOSITORY https://github.com/microsoft/Detours.git
    GIT_TAG v4.0.1
    GIT_SHALLOW TRUE
)
FetchContent_GetProperties(detours)
if(NOT detours_POPULATED)
    FetchContent_Populate(detours)
endif()

# The WinDivert backend is deliberately x64-only. Dependencies are pinned so a
# future upstream change cannot silently alter release binaries.
if(CMAKE_SIZEOF_VOID_P EQUAL 8)
    FetchContent_Declare(
        proxybridge
        GIT_REPOSITORY https://github.com/InterceptSuite/ProxyBridge.git
        GIT_TAG 2929120c66f5f37c2f2abe48770a5e1a8a494b83
        GIT_SHALLOW FALSE
    )
    FetchContent_Declare(
        windivert
        URL https://github.com/basil00/WinDivert/releases/download/v2.2.2/WinDivert-2.2.2-A.zip
        URL_HASH SHA256=63CB41763BB4B20F600B6DE04E991A9C2BE73279E317D4D82F237B150C5F3F15
    )
    FetchContent_GetProperties(proxybridge)
    if(NOT proxybridge_POPULATED)
        FetchContent_Populate(proxybridge)
    endif()
    FetchContent_GetProperties(windivert)
    if(NOT windivert_POPULATED)
        FetchContent_Populate(windivert)
    endif()
    set(WINDIVERT_ROOT "${windivert_SOURCE_DIR}")
    if(EXISTS "${WINDIVERT_ROOT}/WinDivert-2.2.2-A/include/windivert.h")
        set(WINDIVERT_ROOT "${WINDIVERT_ROOT}/WinDivert-2.2.2-A")
    endif()
    if(NOT EXISTS "${WINDIVERT_ROOT}/include/windivert.h")
        message(FATAL_ERROR "The pinned WinDivert archive has an unexpected layout.")
    endif()

    add_library(proxybridge_core SHARED
        ${proxybridge_SOURCE_DIR}/Windows/src/ProxyBridge.c
        ${proxybridge_SOURCE_DIR}/Windows/src/ProxyBridge.h
    )
    target_include_directories(proxybridge_core PRIVATE
        ${proxybridge_SOURCE_DIR}/Windows/src
        ${WINDIVERT_ROOT}/include
    )
    target_compile_definitions(proxybridge_core PRIVATE
        PROXYBRIDGE_EXPORTS _CRT_SECURE_NO_WARNINGS _WINSOCK_DEPRECATED_NO_WARNINGS
        _WIN32_WINNT=0x0601 WIN32_LEAN_AND_MEAN NDEBUG
    )
    target_compile_options(proxybridge_core PRIVATE /O2 /W4 /utf-8 /wd4100 /wd4189 /wd4244 /wd4267 /wd4996)
    target_link_libraries(proxybridge_core PRIVATE
        "${WINDIVERT_ROOT}/x64/WinDivert.lib" ws2_32 iphlpapi
    )
    set_target_properties(proxybridge_core PROPERTIES OUTPUT_NAME "ProxyBridgeCore")

    add_executable(easy_net_windivert ${proxybridge_SOURCE_DIR}/Windows/cli/main.c)
    target_compile_definitions(easy_net_windivert PRIVATE
        _CRT_SECURE_NO_WARNINGS _WINSOCK_DEPRECATED_NO_WARNINGS
        _WIN32_WINNT=0x0601 WIN32_LEAN_AND_MEAN NDEBUG
    )
    target_compile_options(easy_net_windivert PRIVATE /O2 /W4 /utf-8 /wd4100 /wd4189 /wd4244 /wd4267 /wd4996)
    target_link_libraries(easy_net_windivert PRIVATE winhttp shell32 advapi32)
    set_target_properties(easy_net_windivert PROPERTIES OUTPUT_NAME "easy-net-windivert")
endif()

set(DETOURS_SOURCES
    ${detours_SOURCE_DIR}/src/detours.cpp
    ${detours_SOURCE_DIR}/src/modules.cpp
    ${detours_SOURCE_DIR}/src/disasm.cpp
    ${detours_SOURCE_DIR}/src/image.cpp
    ${detours_SOURCE_DIR}/src/creatwth.cpp
    ${detours_SOURCE_DIR}/src/disolx86.cpp
    ${detours_SOURCE_DIR}/src/disolx64.cpp
    ${detours_SOURCE_DIR}/src/disolia64.cpp
    ${detours_SOURCE_DIR}/src/disolarm.cpp
    ${detours_SOURCE_DIR}/src/disolarm64.cpp
)

add_library(detours STATIC ${DETOURS_SOURCES})
target_include_directories(detours PUBLIC ${detours_SOURCE_DIR}/src)
target_compile_definitions(detours PRIVATE WIN32_LEAN_AND_MEAN _WIN32_WINNT=0x0601)
target_compile_options(detours PRIVATE /W3 /wd4100 /wd4127 /wd4189 /wd4242 /wd4244 /wd4701)

add_library(easy_net_hook_dll SHARED
    src/child_injection.h
    src/config_ipc.h
    src/dns_resolver.cpp
    src/dns_resolver.h
    src/hook.cpp
    src/socks5_protocol.h
)
target_compile_features(easy_net_hook_dll PRIVATE cxx_std_20)
target_compile_definitions(easy_net_hook_dll PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX _WIN32_WINNT=0x0601)
target_compile_options(easy_net_hook_dll PRIVATE /W4 /permissive- /EHsc /utf-8)
target_link_options(easy_net_hook_dll PRIVATE "/export:DetourFinishHelperProcess,@1,NONAME")
target_link_libraries(easy_net_hook_dll PRIVATE detours ws2_32 normaliz)
set_target_properties(easy_net_hook_dll PROPERTIES OUTPUT_NAME "easy-net-hook")

add_executable(easy_net_hook_launcher
    src/easy_net_hook.manifest
    src/browser_proxy.h
    src/chatgpt_package.h
    src/config_ipc.h
    src/history_store.h
    src/launcher.cpp
    src/lite_api.h
    src/socks5_health.h
    src/tun_config.h
    src/wechat_supervisor.h
    src/windivert_profile.h
    src/dns_resolver.cpp
    src/dns_resolver.h
)
target_compile_features(easy_net_hook_launcher PRIVATE cxx_std_20)
target_include_directories(easy_net_hook_launcher PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_compile_definitions(easy_net_hook_launcher PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE _WIN32_WINNT=0x0A00)
target_compile_options(easy_net_hook_launcher PRIVATE
    "$<$<COMPILE_LANGUAGE:CXX>:/W4;/permissive-;/EHsc;/utf-8>")
target_link_libraries(easy_net_hook_launcher PRIVATE detours advapi32 comctl32 comdlg32 ole32 shell32 winhttp ws2_32)
set_target_properties(easy_net_hook_launcher PROPERTIES OUTPUT_NAME "easy-net-hook")
add_dependencies(easy_net_hook_launcher easy_net_hook_dll)

set_property(TARGET detours easy_net_hook_dll easy_net_hook_launcher PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
if(TARGET proxybridge_core)
    set_property(TARGET proxybridge_core easy_net_windivert PROPERTY
        MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
endif()

enable_testing()
add_test(NAME launcher_help_test COMMAND easy_net_hook_launcher --help)
set_tests_properties(launcher_help_test PROPERTIES
    PASS_REGULAR_EXPRESSION "wechat-existing")
add_test(NAME launcher_cursor_help_test COMMAND easy_net_hook_launcher --help)
set_tests_properties(launcher_cursor_help_test PROPERTIES
    PASS_REGULAR_EXPRESSION "cursor-isolated")
add_test(NAME launcher_browser_help_test COMMAND easy_net_hook_launcher --help)
set_tests_properties(launcher_browser_help_test PROPERTIES
    PASS_REGULAR_EXPRESSION "browser-isolated")
add_executable(socks5_protocol_test tests/socks5_protocol_test.cpp src/socks5_protocol.h)
target_compile_features(socks5_protocol_test PRIVATE cxx_std_20)
target_compile_definitions(socks5_protocol_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX)
target_compile_options(socks5_protocol_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
target_link_libraries(socks5_protocol_test PRIVATE ws2_32)
set_property(TARGET socks5_protocol_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME socks5_protocol_test COMMAND socks5_protocol_test)

add_executable(dns_resolver_test
    tests/dns_resolver_test.cpp
    src/dns_resolver.cpp
    src/dns_resolver.h
)
target_compile_features(dns_resolver_test PRIVATE cxx_std_20)
target_compile_definitions(dns_resolver_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX _WIN32_WINNT=0x0601)
target_compile_options(dns_resolver_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
target_link_libraries(dns_resolver_test PRIVATE ws2_32)
set_property(TARGET dns_resolver_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME dns_resolver_test COMMAND dns_resolver_test)

add_executable(config_ipc_test tests/config_ipc_test.cpp src/config_ipc.h)
target_compile_features(config_ipc_test PRIVATE cxx_std_20)
target_compile_definitions(config_ipc_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE)
target_compile_options(config_ipc_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET config_ipc_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME config_ipc_test COMMAND config_ipc_test)

add_executable(browser_proxy_test tests/browser_proxy_test.cpp src/browser_proxy.h)
target_compile_features(browser_proxy_test PRIVATE cxx_std_20)
target_compile_definitions(browser_proxy_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE)
target_compile_options(browser_proxy_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
target_link_libraries(browser_proxy_test PRIVATE ws2_32)
set_property(TARGET browser_proxy_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME browser_proxy_test COMMAND browser_proxy_test)

add_executable(child_injection_test tests/child_injection_test.cpp src/child_injection.h)
target_compile_features(child_injection_test PRIVATE cxx_std_20)
target_compile_definitions(child_injection_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE)
target_compile_options(child_injection_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET child_injection_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME child_injection_test COMMAND child_injection_test)

add_executable(history_store_test tests/history_store_test.cpp src/history_store.h)
target_compile_features(history_store_test PRIVATE cxx_std_20)
target_compile_definitions(history_store_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE)
target_compile_options(history_store_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET history_store_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME history_store_test COMMAND history_store_test)

add_executable(tun_config_test tests/tun_config_test.cpp src/tun_config.h)
target_compile_features(tun_config_test PRIVATE cxx_std_20)
target_compile_options(tun_config_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET tun_config_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME tun_config_test COMMAND tun_config_test)

add_executable(windivert_profile_test
    tests/windivert_profile_test.cpp
    src/windivert_profile.h
    src/tun_config.h
)
target_compile_features(windivert_profile_test PRIVATE cxx_std_20)
target_compile_options(windivert_profile_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET windivert_profile_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME windivert_profile_test COMMAND windivert_profile_test)

add_executable(socks5_health_test tests/socks5_health_test.cpp src/socks5_health.h src/tun_config.h)
target_compile_features(socks5_health_test PRIVATE cxx_std_20)
target_compile_definitions(socks5_health_test PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX)
target_compile_options(socks5_health_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
target_link_libraries(socks5_health_test PRIVATE ws2_32)
set_property(TARGET socks5_health_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME socks5_health_test COMMAND socks5_health_test)

add_executable(wechat_supervisor_test
    tests/wechat_supervisor_test.cpp
    src/wechat_supervisor.h
    src/tun_config.h
)
target_compile_features(wechat_supervisor_test PRIVATE cxx_std_20)
target_compile_options(wechat_supervisor_test PRIVATE /W4 /permissive- /EHsc /UNDEBUG)
set_property(TARGET wechat_supervisor_test PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_test(NAME wechat_supervisor_test COMMAND wechat_supervisor_test)

add_custom_command(TARGET easy_net_hook_launcher POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
        $<TARGET_FILE:easy_net_hook_dll>
        $<TARGET_FILE_DIR:easy_net_hook_launcher>
    COMMAND ${CMAKE_COMMAND} -E make_directory
        $<TARGET_FILE_DIR:easy_net_hook_launcher>/THIRD-PARTY-LICENSES
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
        ${detours_SOURCE_DIR}/LICENSE.md
        $<TARGET_FILE_DIR:easy_net_hook_launcher>/THIRD-PARTY-LICENSES/Detours-LICENSE.md
)

find_program(GO_EXECUTABLE go)
if(GO_EXECUTABLE)
    if(CMAKE_SIZEOF_VOID_P EQUAL 8)
        set(EASY_NET_LITE_GOARCH amd64)
    else()
        set(EASY_NET_LITE_GOARCH 386)
    endif()
    set(EASY_NET_LITE_VERSION "dev")
    if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/VERSION")
        file(READ "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/VERSION" EASY_NET_LITE_VERSION)
        string(STRIP "${EASY_NET_LITE_VERSION}" EASY_NET_LITE_VERSION)
    endif()
    file(GLOB_RECURSE EASY_NET_LITE_GO_SOURCES CONFIGURE_DEPENDS
        "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/*.go"
    )
    set(EASY_NET_LITE_OUTPUT
        "${CMAKE_CURRENT_BINARY_DIR}/$<CONFIG>/Easy-Net-Lite.exe")
    add_custom_command(OUTPUT "${EASY_NET_LITE_OUTPUT}"
        COMMAND ${CMAKE_COMMAND} -E make_directory
            "${CMAKE_CURRENT_BINARY_DIR}/$<CONFIG>"
        COMMAND powershell -NoProfile -ExecutionPolicy Bypass -File
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/scripts/generate-windows-resource.ps1"
            -Architecture ${EASY_NET_LITE_GOARCH}
        COMMAND ${CMAKE_COMMAND} -E env
            CGO_ENABLED=0
            GOOS=windows
            GOARCH=${EASY_NET_LITE_GOARCH}
            --
            ${GO_EXECUTABLE} build -trimpath
            "-ldflags=-s -w -H=windowsgui -X easy-net/client-lite/internal/version.Value=${EASY_NET_LITE_VERSION}"
            -o "${EASY_NET_LITE_OUTPUT}"
            ./cmd/easy-net
        COMMAND ${CMAKE_COMMAND} -E rm -f
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/cmd/easy-net/easy_net_lite_windows.syso"
        DEPENDS
            ${EASY_NET_LITE_GO_SOURCES}
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/go.mod"
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/go.sum"
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/VERSION"
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/assets/easy-net-lite.ico"
            "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite/scripts/generate-windows-resource.ps1"
        WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/../client-lite"
        COMMENT "Building full Easy-Net Lite tray application"
        VERBATIM
    )
    add_custom_target(easy_net_lite ALL DEPENDS "${EASY_NET_LITE_OUTPUT}")
    add_dependencies(easy_net_hook_launcher easy_net_lite)
else()
    message(WARNING "Go was not found; Easy-Net-Lite.exe will be unavailable in the combined package.")
endif()

if(TARGET proxybridge_core)
    add_dependencies(easy_net_hook_launcher proxybridge_core easy_net_windivert)
    add_custom_command(TARGET easy_net_hook_launcher POST_BUILD
        COMMAND ${CMAKE_COMMAND} -E make_directory
            $<TARGET_FILE_DIR:easy_net_hook_launcher>/windivert
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
            $<TARGET_FILE:proxybridge_core>
            $<TARGET_FILE:easy_net_windivert>
            ${WINDIVERT_ROOT}/x64/WinDivert.dll
            ${WINDIVERT_ROOT}/x64/WinDivert64.sys
            $<TARGET_FILE_DIR:easy_net_hook_launcher>/windivert
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
            ${proxybridge_SOURCE_DIR}/LICENSE
            $<TARGET_FILE_DIR:easy_net_hook_launcher>/THIRD-PARTY-LICENSES/ProxyBridge-LICENSE.txt
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
            ${WINDIVERT_ROOT}/LICENSE
            $<TARGET_FILE_DIR:easy_net_hook_launcher>/THIRD-PARTY-LICENSES/WinDivert-LICENSE.txt
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
            ${WINDIVERT_ROOT}/VERSION
            $<TARGET_FILE_DIR:easy_net_hook_launcher>/windivert/WinDivert-VERSION.txt
    )
endif()
