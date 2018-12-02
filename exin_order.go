package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/MixinNetwork/bot-api-go-client"
	"github.com/MixinNetwork/go-number"
	"github.com/vmihailenco/msgpack"

	"github.com/satori/go.uuid"
)

const (
	ExinCore = "61103d28-3ac2-44a2-ae34-bd956070dab1"
)

type ExinOrderAction struct {
	A uuid.UUID // asset uuid
}

func (order *ExinOrderAction) Pack() string {
	pack, err := msgpack.Marshal(order)
	if err != nil {
		return ""
	}

	return base64.StdEncoding.EncodeToString(pack)
}

func (order *ExinOrderAction) Unpack(memo string) error {
	parsedpack, err := base64.StdEncoding.DecodeString(memo)
	if err != nil {
		return err
	}
	return msgpack.Unmarshal(parsedpack, order)
}

func ExinTrade(amount float64, send, get string) (string, error) {
	trace := uuid.Must(uuid.NewV4()).String()
	order := ExinOrderAction{
		A: uuid.Must(uuid.FromString(get)),
	}
	transfer := bot.TransferInput{
		AssetId:     send,
		RecipientId: ExinCore,
		Amount:      number.FromFloat(amount),
		TraceId:     trace,
		Memo:        order.Pack(),
	}
	//fmt.Println("transfer", transfer)
	return trace, bot.CreateTransfer(context.TODO(), &transfer, ClientId, SessionId, PrivateKey, PinCode, PinToken)
}

func ExinTradeMessager(side string, amount float64, base, quote string) (string, error) {
	memo := fmt.Sprintf("ExinOne %s/%s %s", Who(base), Who(quote), side)
	trace := uuid.Must(uuid.NewV4()).String()
	var asset string
	if side == "buy" {
		asset = quote
	} else if side == "sell" {
		asset = base
	} else {
		panic("invlid type")
	}
	transfer := bot.TransferInput{
		AssetId:     asset,
		RecipientId: ExinCore,
		Amount:      number.FromFloat(amount),
		TraceId:     trace,
		Memo:        memo,
	}
	return trace, bot.CreateTransfer(context.TODO(), &transfer, ClientId, SessionId, PrivateKey, PinCode, PinToken)
}