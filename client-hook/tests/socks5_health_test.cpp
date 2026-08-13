#include <cassert>

#include "../src/socks5_health.h"

int main() {
    easy_net::tun::Endpoint invalid{"not-an-ip", 1080};
    assert(!easy_net::socks5_health::Responsive(invalid, 10));
    return 0;
}
