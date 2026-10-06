package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/nbd-wtf/go-nostr"
)

func main() {
	secretKey, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		panic(err)
	}
	publicKey, err := nostr.GetPublicKey(strings.TrimSpace(secretKey))
	if err != nil {
		panic(err)
	}
	fmt.Print(publicKey)
}
