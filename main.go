package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"git.aqq.me/go/app/appconf"
	"git.aqq.me/go/app/applog"
	"git.aqq.me/go/app/launcher"
	"github.com/go-redis/redis"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/iph0/conf"
	"github.com/iph0/conf/envconf"
	"github.com/iph0/conf/fileconf"
	"golang.org/x/net/proxy"
)

type redisConf struct {
	Addrs string
}

type tgConf struct {
	Token string
	URL   string
	Path  string
	Proxy string
}

type beckaConf struct {
	Telegram tgConf
	Redis    redisConf
}

func init() {
	fileLdr := fileconf.NewLoader("etc", "/etc")
	envLdr := envconf.NewLoader()

	appconf.RegisterLoader("file", fileLdr)
	appconf.RegisterLoader("env", envLdr)

	appconf.Require("file:becka.yml")
	appconf.Require("env:^BECKA_")
}

func main() {
	launcher.Run(func() error {
		cnfMap := appconf.GetConfig()["becka"]

		var cnf beckaConf
		err := conf.Decode(cnfMap, &cnf)
		if err != nil {
			return err
		}

		addrs := strings.Split(cnf.Redis.Addrs, ",")

		ropt := &redis.ClusterOptions{
			Addrs: addrs,
		}

		rDB := redis.NewClusterClient(ropt)

		log := applog.GetLogger().Sugar()

		httpTransport := &http.Transport{}

		if len(cnf.Telegram.Proxy) > 0 {
			dialer, err := proxy.SOCKS5("tcp", cnf.Telegram.Proxy, nil, proxy.Direct)
			if err != nil {
				return err
			}

			httpTransport.Dial = dialer.Dial
		}

		httpClient := &http.Client{Transport: httpTransport}

		bot, err := tgbotapi.NewBotAPIWithClient(cnf.Telegram.Token, httpClient)
		if err != nil {
			return err
		}

		res, err := bot.SetWebhook(tgbotapi.NewWebhook(cnf.Telegram.URL + cnf.Telegram.Path))
		if err != nil {
			return err
		}

		log.Debug(res.Description)

		updates := bot.ListenForWebhook("/" + cnf.Telegram.Path)

		http.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "ok")
		})

		go http.ListenAndServe("0.0.0.0:8080", nil)

		go func() {
			for upd := range updates {
				if upd.Message == nil {
					continue
				}

				if upd.Message.Sticker != nil {
					key := fmt.Sprintf("becka{%d}", upd.Message.From.ID)
					res, err := rDB.Incr(key).Result()
					if err != nil {
						continue
					}

					if res == 1 {
						err = rDB.Expire(key, time.Hour*24).Err()
						if err != nil {
							continue
						}
					}

					if res > 10 {
						log.Debugf("Delete %s for %d", upd.Message.From.UserName, res)

						_, err := bot.DeleteMessage(tgbotapi.DeleteMessageConfig{
							ChatID:    upd.Message.Chat.ID,
							MessageID: upd.Message.MessageID,
						})

						if err != nil {
							log.Error(err)
						}
					}
				}
			}
		}()

		return nil
	})
}
