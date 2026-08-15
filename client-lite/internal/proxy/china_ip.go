package proxy

import (
	_ "embed"
	"net"
	"net/netip"
	"strings"
)

//go:embed cn_apnic.txt
var chinaPrefixData string

type ipTrieNode struct {
	children [2]*ipTrieNode
	terminal bool
}

type ipTrie struct {
	v4 ipTrieNode
	v6 ipTrieNode
}

var chinaIPTrie = buildChinaIPTrie(chinaPrefixData)

func buildChinaIPTrie(data string) ipTrie {
	var trie ipTrie
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			continue
		}
		prefix = prefix.Masked()
		address := prefix.Addr()
		root := &trie.v6
		bits := address.As16()
		if address.Is4() {
			root = &trie.v4
			v4 := address.As4()
			bits = [16]byte{v4[0], v4[1], v4[2], v4[3]}
		}
		node := root
		for index := 0; index < prefix.Bits(); index++ {
			bit := (bits[index/8] >> (7 - uint(index%8))) & 1
			if node.children[bit] == nil {
				node.children[bit] = &ipTrieNode{}
			}
			node = node.children[bit]
		}
		node.terminal = true
	}
	return trie
}

func isChinaDestination(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	node := &chinaIPTrie.v6
	bits := address.As16()
	bitCount := 128
	if address.Is4() {
		node = &chinaIPTrie.v4
		v4 := address.As4()
		bits = [16]byte{v4[0], v4[1], v4[2], v4[3]}
		bitCount = 32
	}
	for index := 0; index < bitCount; index++ {
		if node.terminal {
			return true
		}
		bit := (bits[index/8] >> (7 - uint(index%8))) & 1
		node = node.children[bit]
		if node == nil {
			return false
		}
	}
	return node.terminal
}
