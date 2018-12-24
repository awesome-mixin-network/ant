package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/MixinNetwork/bot-api-go-client"
	"github.com/emirpasic/gods/lists/arraylist"
	"github.com/hokaccha/go-prettyjson"
	uuid "github.com/satori/go.uuid"
	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

const (
	ProfitThreshold = 0.010 / (1 - OceanFee) / (1 - ExinFee)
	OceanFee        = 0.001
	ExinFee         = 0.000
	OrderExpireTime = int64(5 * time.Second)
)

type ProfitEvent struct {
	ID            string          `json:"-"`
	Category      string          `json:"category"`
	Price         decimal.Decimal `json:"price"`
	Profit        decimal.Decimal `json:"profit"`
	Amount        decimal.Decimal `json:"amount"`
	Min           decimal.Decimal `json:"min"`
	Max           decimal.Decimal `json:"max"`
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	CreatedAt     time.Time       `json:"created_at"`
	Expire        int64           `json:"expire"`
	BaseAmount    decimal.Decimal `json:"base_amount"`
	QuoteAmount   decimal.Decimal `json:"quote_amount"`
	ExchangeOrder string          `json:"exchange_order"`
}

type Ant struct {
	//是否开启交易
	enableOcean bool
	enableExin  bool
	//发现套利机会
	event chan *ProfitEvent
	//所有交易的snapshot_id
	snapshots map[string]bool
	//机器人向ocean.one交易的trace_id
	orders map[string]bool
	//买单和卖单的红黑树，生成深度用
	books      map[string]*OrderBook
	orderQueue *arraylist.List
	assetsLock sync.Mutex
	assets     map[string]decimal.Decimal
	client     *bot.BlazeClient
}

func NewAnt(ocean, exin bool) *Ant {
	return &Ant{
		enableOcean: ocean,
		enableExin:  exin,
		event:       make(chan *ProfitEvent, 10),
		snapshots:   make(map[string]bool, 0),
		orders:      make(map[string]bool, 0),
		books:       make(map[string]*OrderBook, 0),
		assets:      make(map[string]decimal.Decimal, 0),
		orderQueue:  arraylist.New(),
		client:      bot.NewBlazeClient(ClientId, SessionId, PrivateKey),
	}
}

func UuidWithString(str string) string {
	h := md5.New()
	io.WriteString(h, str)
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x30
	sum[8] = (sum[8] & 0x3f) | 0x80
	return uuid.FromBytesOrNil(sum).String()
}

func (ant *Ant) OnBlaseMessage(base, quote string) *OrderBook {
	pair := base + "-" + quote
	ant.books[pair] = NewBook(base, quote)
	return ant.books[pair]
}

func (ant *Ant) Clean() {
	for trace, ok := range ant.orders {
		if !ok {
			OceanCancel(trace)
		}
	}
	for it := ant.orderQueue.Iterator(); it.Next(); {
		event := it.Value().(*ProfitEvent)
		v, _ := prettyjson.Marshal(event)
		log.Println("event:", string(v))
	}
}

func (ant *Ant) trade(e *ProfitEvent) error {
	exchangeOrder := UuidWithString(e.ID + OceanCore)
	if _, ok := ant.orders[exchangeOrder]; ok {
		return nil
	}

	defer func() {
		go func(trace string) {
			select {
			case <-time.After(time.Duration(OrderExpireTime)):
				if err := OceanCancel(trace); err == nil {
					ant.orders[exchangeOrder] = true
				}
			}
		}(exchangeOrder)

		go ant.Notice(context.TODO(), *e, MixinMessageID)
	}()

	if !ant.enableOcean {
		ant.orders[exchangeOrder] = true
		return nil
	}

	amount := e.Amount
	ant.assetsLock.Lock()
	baseBalance := ant.assets[e.Base]
	quoteBalance := ant.assets[e.Quote]
	ant.assetsLock.Unlock()
	if amount.GreaterThan(baseBalance) {
		amount = baseBalance
	}
	if e.Category == PageSideBid {
		amount = e.Amount.Mul(e.Price)
		if amount.GreaterThan(quoteBalance) {
			amount = quoteBalance
		}
	}

	ant.orders[exchangeOrder] = false
	_, err := OceanTrade(e.Category, e.Price.String(), amount.String(), OrderTypeLimit, e.Base, e.Quote, exchangeOrder)
	if err != nil {
		return err
	}

	amount = amount.Mul(decimal.NewFromFloat(-1.0))
	if e.Category == PageSideBid {
		e.QuoteAmount = amount
	} else {
		e.BaseAmount = amount
	}
	e.ExchangeOrder = exchangeOrder
	ant.orderQueue.Add(e)
	return nil
}

func LimitAmount(amount, balance, min, max decimal.Decimal) decimal.Decimal {
	if amount.LessThanOrEqual(min) {
		log.Errorf("amount too small, %v < min : %v", amount, min)
		return decimal.Zero
	}

	less := max
	if max.GreaterThan(balance) {
		less = balance
	}
	if amount.GreaterThan(less) {
		return less
	}
	return amount
}

func (ant *Ant) OnExpire(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			expired := make([]int, 0)
			for it := ant.orderQueue.Iterator(); it.Next(); {
				event := it.Value().(*ProfitEvent)
				if event.CreatedAt.Add(time.Duration(event.Expire)).Before(time.Now()) {
					expired = append(expired, it.Index())
					amount := event.BaseAmount
					send, side := event.Base, PageSideAsk
					if !amount.IsPositive() {
						amount = event.QuoteAmount
						send, side = event.Quote, PageSideBid
						if !amount.IsPositive() {
							continue
						}
					}

					ant.assetsLock.Lock()
					balance := ant.assets[send]
					ant.assetsLock.Unlock()
					limited := LimitAmount(amount, balance, event.Min, event.Max)
					if send == event.Quote {
						limited = LimitAmount(amount, balance, event.Min.Mul(event.Price), event.Max.Mul(event.Price))
					}

					if !limited.IsPositive() {
						log.Errorf("%s, balance: %v, min: %v, send: %v,amount: %v, limited: %v", Who(send), balance, event.Min, send, amount, limited)
					} else {
						if _, err := ExinTrade(side, limited.String(), event.Base, event.Quote); err != nil {
							log.Error(err)
							continue
						}
					}
					ant.orders[event.ExchangeOrder] = true
				}
			}
			for _, idx := range expired {
				ant.orderQueue.Remove(idx)
			}
		}
	}
}

func (ant *Ant) HandleSnapshot(ctx context.Context, s *Snapshot) error {
	if s.SnapshotId == ExinCore {
		return nil
	}
	amount, _ := decimal.NewFromString(s.Amount)
	if amount.IsNegative() {
		return nil
	}

	for it := ant.orderQueue.Iterator(); it.Next(); {
		event := it.Value().(*ProfitEvent)
		var order OceanTransfer
		if err := order.Unpack(s.Data); err != nil {
			return err
		}

		if event.ExchangeOrder != order.A.String() &&
			event.ExchangeOrder != order.B.String() &&
			event.ExchangeOrder != order.O.String() {
			continue
		}

		if s.AssetId == event.Base {
			event.BaseAmount = event.BaseAmount.Add(amount)
		} else if s.AssetId == event.Quote {
			event.QuoteAmount = event.QuoteAmount.Add(amount)
		} else {
			panic(s.AssetId)
		}
		it.End()
	}
	return nil
}

func (ant *Ant) Trade(ctx context.Context) error {
	if ant.enableExin {
		go ant.OnExpire(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-ant.event:
			if err := ant.trade(e); err != nil {
				log.Error(err)
			}
		}
	}
}

func (ant *Ant) Watching(ctx context.Context, base, quote string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if otc, err := GetExinDepth(ctx, base, quote); err == nil {
				pair := base + "-" + quote
				if exchange := ant.books[pair].GetDepth(3); exchange != nil {
					if len(exchange.Bids) > 0 && len(otc.Asks) > 0 {
						ant.Inspect(ctx, exchange.Bids[0], otc.Asks[0], base, quote, PageSideBid, OrderExpireTime)
					}

					if len(exchange.Asks) > 0 && len(otc.Bids) > 0 {
						ant.Inspect(ctx, exchange.Asks[0], otc.Bids[0], base, quote, PageSideAsk, OrderExpireTime)
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (ant *Ant) Inspect(ctx context.Context, exchange, otc Order, base, quote string, side string, expire int64) {
	var category string
	if side == PageSideBid {
		category = PageSideAsk
	} else if side == PageSideAsk {
		category = PageSideBid
	} else {
		panic(category)
	}

	profit := exchange.Price.Sub(otc.Price).Div(otc.Price)
	if side == PageSideAsk {
		profit = profit.Mul(decimal.NewFromFloat(-1.0))
	}

	msg := fmt.Sprintf("%s --amount:%10.8v, ocean price: %10.8v, exin price: %10.8v, profit: %10.8v, %5v/%5v", side, exchange.Amount.String(), exchange.Price, otc.Price, profit, Who(base), Who(quote))
	log.Debug(msg)
	if profit.LessThan(decimal.NewFromFloat(ProfitThreshold)) {
		return
	}
	log.Info(msg)

	id := UuidWithString(ClientId + exchange.Price.String() + exchange.Amount.String() + category + Who(base) + Who(quote))
	event := ProfitEvent{
		ID:       id,
		Category: category,
		Price:    exchange.Price,
		//多付款，保证扣完手续费后能全买下
		Amount:      exchange.Amount.Mul(decimal.NewFromFloat(1.1)).Round(8),
		Min:         otc.Min,
		Max:         otc.Max,
		Profit:      profit,
		Base:        base,
		Quote:       quote,
		Expire:      expire,
		CreatedAt:   time.Now(),
		BaseAmount:  decimal.Zero,
		QuoteAmount: decimal.Zero,
	}
	select {
	case ant.event <- &event:
	case <-time.After(5 * time.Second):
	}
	return
}

func (ant *Ant) UpdateBalance(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	update := func() {
		assets, err := ReadAssets(ctx)
		if err != nil {
			return
		}
		for asset, balance := range assets {
			b, err := decimal.NewFromString(balance)
			if err == nil && !b.Equal(ant.assets[asset]) {
				ant.assetsLock.Lock()
				ant.assets[asset] = b
				ant.assetsLock.Unlock()
			}
		}
	}

	update()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			update()
		}
	}
}
