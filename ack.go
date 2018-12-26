package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	bot "github.com/MixinNetwork/bot-api-go-client"
	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

const (
	SubcribedUser = "subcriberd_user"
	OceanWebsite  = "https://mixcoin.one"
	ExinWebsite   = "https://exinone.com/#/exchange/flash/flashTakeOrder?uuid=%d"
)

var PairIndex = map[string]int{
	"BTC/USDT": 15,
	"ETH/USDT": 17,
	"BCH/USDT": 16,
	"EOS/USDT": 18,
	"ETH/BTC":  19,
	"BCH/BTC":  21,
	"EOS/BTC":  20,
	"XIN/BTC":  4,
	"EOS/ETH":  22,
}

func (ant *Ant) OnMessage(ctx context.Context, msgView bot.MessageView, userId string) error {
	if msgView.Category == bot.MessageCategoryPlainText && msgView.ConversationId == bot.UniqueConversationId(ClientId, msgView.UserId) {
		data, err := base64.StdEncoding.DecodeString(msgView.Data)
		if err != nil {
			return err
		}
		switch strings.ToLower(string(data)) {
		case "whoisyourdaddy":
			assets, err := ReadAssets(ctx)
			if err != nil {
				return err
			}
			out := make(map[string]string, 0)
			for asset, balance := range assets {
				if amount, _ := strconv.ParseFloat(balance, 64); amount > 0.0 {
					out[Who(asset)] = balance
				}
			}
			bt, err := json.Marshal(out)
			if err != nil {
				return err
			}
			ant.client.SendPlainText(ctx, msgView, string(bt))
		case "sub":
			if _, err := Redis(ctx).SAdd(SubcribedUser, msgView.UserId).Result(); err != nil {
				log.Println("Add user err", err)
			}
			ant.client.SendPlainText(ctx, msgView, "Thanks for your attention.\n You will get a notification if you can benefit from the price differences below.")
			ocean := bot.Button{Label: "Mixcoin", Action: OceanWebsite, Color: "#2e8b57"}
			exin := bot.Button{Label: "ExinOne", Action: fmt.Sprintf(ExinWebsite, 15), Color: "#bc8f8f"}
			if err := ant.client.SendAppButtons(ctx, msgView.ConversationId, msgView.UserId, ocean, exin); err != nil {
				log.Println("Trade error", err)
			}
		case "unsub":
			if _, err := Redis(ctx).SRem(SubcribedUser, msgView.UserId).Result(); err != nil {
				log.Println("Remove user err", err)
			}
			ant.client.SendPlainText(ctx, msgView, "Goodbye! But I am sure you will come back.")
		default:
			reply := strings.Replace(string(data), "么", "", -1)
			reply = strings.Replace(reply, "吗", "", -1)
			reply = strings.Replace(reply, "嘛", "", -1)
			reply = strings.Replace(reply, "啊", "", -1)
			reply = strings.Replace(reply, "?", "!", -1)
			reply = strings.Replace(reply, "？", "！", -1)
			ant.client.SendPlainText(ctx, msgView, reply)
		}
	}
	return nil
}

func (ant *Ant) Notice(ctx context.Context, event ProfitEvent) error {
	users, err := Redis(ctx).SMembers(SubcribedUser).Result()
	if err != nil {
		return err
	}
	actions := map[string]string{
		PageSideBid: " Buy in Mixcoin",
		PageSideAsk: "Sell in Mixcoin",
	}

	template := "Action:  %-10s\nPair:         %-10s\nPrice:       %-10.8s\nAmount:    %-10s\nProfit:   %8s%%"
	pair := Who(event.Base) + "/" + Who(event.Quote)
	ocean := bot.Button{Label: "Mixcoin", Action: OceanWebsite, Color: "#2e8b57"}
	exin := bot.Button{Label: "ExinOne", Action: fmt.Sprintf(ExinWebsite, PairIndex[pair]), Color: "#bc8f8f"}
	msg := fmt.Sprintf(template, actions[event.Category], pair, event.Price.String(),
		event.Amount.String(), event.Profit.Mul(decimal.NewFromFloat(100.0)).Round(2).String())

	for _, user := range users {
		msgView := bot.MessageView{
			ConversationId: bot.UniqueConversationId(ClientId, user),
			UserId:         user,
		}

		if err := ant.client.SendPlainText(ctx, msgView, msg); err != nil {
			log.Println("Send message error", err)
		}

		if err := ant.client.SendAppButtons(ctx, msgView.ConversationId, msgView.UserId, ocean, exin); err != nil {
			log.Println("Trade error", err)
		}
	}
	return nil
}

func (ant *Ant) PollMixinMessage(ctx context.Context) {
	for {
		if err := ant.client.Loop(ctx, ant); err != nil {
			log.Println(err)
		}
		time.Sleep(1 * time.Second)
	}
}

func SearchUser(ctx context.Context, id string) (string, error) {
	method, uri := "GET", "/search/"+id
	token, err := bot.SignAuthenticationToken(ClientId, SessionId, PrivateKey, "GET", uri, "")
	if err != nil {
		return "", err
	}
	bt, err := bot.Request(ctx, method, uri, nil, token)
	if err != nil {
		return "", err
	}

	var resp struct {
		Data struct {
			UserId string `json:"user_id"`
		} `json:"data"`
	}

	err = json.Unmarshal(bt, &resp)
	return resp.Data.UserId, err
}
