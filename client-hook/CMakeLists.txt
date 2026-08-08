cmake_minimum_required(VERSION 3.24)

project(easy_net_hook LANGUAGES CXX RC)

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
    src/easy_net_hook.rc
    src/browser_proxy.h
    src/config_ipc.h
    src/history_store.h
    src/launcher.cpp
    src/launcher_gui.cpp
    src/launcher_gui.h
    src/resource.h
    src/dns_resolver.cpp
    src/dns_resolver.h
)
target_compile_features(easy_net_hook_launcher PRIVATE cxx_std_20)
target_include_directories(easy_net_hook_launcher PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_compile_definitions(easy_net_hook_launcher PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX UNICODE _UNICODE _WIN32_WINNT=0x0A00)
target_compile_options(easy_net_hook_launcher PRIVATE
    "$<$<COMPILE_LANGUAGE:CXX>:/W4;/permissive-;/EHsc;/utf-8>")
target_link_libraries(easy_net_hook_launcher PRIVATE detours advapi32 comctl32 comdlg32 ole32 shell32 ws2_32)
set_target_properties(easy_net_hook_launcher PROPERTIES OUTPUT_NAME "easy-net-hook")
add_dependencies(easy_net_hook_launcher easy_net_hook_dll)

set_property(TARGET detours easy_net_hook_dll easy_net_hook_launcher PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")

enable_testing()
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
