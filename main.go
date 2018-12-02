package main

import (
	"context"

	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetLevel(log.DebugLevel)
	ant := Ant{
		e: make(chan Event, 0),
	}
	ctx := context.Background()

	for _, baseSymbol := range []string{"BTC", "EOS", "XIN", "ETH"} {
		for _, quoteSymbol := range []string{"USDT", "BTC", "ETH"} {
			base := GetAssetId(baseSymbol)
			quote := GetAssetId(quoteSymbol)
			go ant.Watching(ctx, base, quote)
		}
	}
	ant.Run()
}