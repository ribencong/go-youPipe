package client

import (
	"encoding/json"
	"fmt"
	"github.com/youpipe/go-youPipe/account"
	"github.com/youpipe/go-youPipe/service"
	"net"
	"sort"
	"sync"
	"time"
)

type Config struct {
	Addr        string
	Cipher      string
	LocalServer string
	License     string
	Services    []string
}

type Client struct {
	*account.Account
	proxyServer net.Listener
	aesKey      account.PipeCryptKey
	license     *service.License
	curService  *service.ServeNodeId
	payCh       *PayChannel
}

func NewClientWithoutCheck(loalSer string, acc *account.Account,
	lic *service.License, server *service.ServeNodeId) (*Client, error) {

	ls, err := net.Listen("tcp", loalSer)
	if err != nil {
		return nil, err
	}

	if lic.UserAddr != acc.Address.ToString() {
		return nil, fmt.Errorf("license and account address are not same")
	}

	c := &Client{
		Account:     acc,
		proxyServer: ls,
		curService:  server,
	}
	if err := c.Key.GenerateAesKey(&c.aesKey, server.ID.ToPubKey()); err != nil {
		return nil, err
	}

	return c, nil
}

func NewClient(conf *Config, password string) (*Client, error) {

	ls, err := net.Listen("tcp", conf.LocalServer)
	if err != nil {
		return nil, err
	}

	acc, err := account.AccFromString(conf.Addr, conf.Cipher, password)
	if err != nil {
		return nil, err
	}

	l, err := service.ParseLicense(conf.License)
	if err != nil {
		return nil, err
	}

	if l.UserAddr != acc.Address.ToString() {
		return nil, fmt.Errorf("license and account address are not same")
	}

	mi := findBestPath(conf.Services)
	if mi == nil {
		return nil, fmt.Errorf("no valid service")
	}

	c := &Client{
		Account:     acc,
		proxyServer: ls,
		license:     l,
		curService:  mi,
	}

	if err := c.Key.GenerateAesKey(&c.aesKey, mi.ID.ToPubKey()); err != nil {
		return nil, err
	}

	if err := c.createPayChannel(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Running() error {

	go c.payCh.payMonitor()

	go c.Proxying()
	err := <-c.payCh.done
	return err
}

func (c *Client) createPayChannel() error {

	addr := c.curService.TONetAddr()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	data, err := json.Marshal(c.license)
	if err != nil {
		return nil
	}

	hs := &service.YPHandShake{
		CmdType: service.CmdPayChanel,
		Sig:     c.Sign(data),
		Lic:     c.license,
	}

	jsonConn := &service.JsonConn{Conn: conn}
	if err := jsonConn.Syn(hs); err != nil {
		return err
	}

	c.payCh = &PayChannel{
		conn:    jsonConn,
		done:    make(chan error),
		minerID: c.curService.ID,
		priKey:  c.Key.PriKey,
	}

	return nil
}

func (c *Client) Close() {

}

func findBestPath(paths []string) *service.ServeNodeId {

	var locker sync.Mutex
	s := make([]*service.ServeNodeId, 0)

	var waiter sync.WaitGroup
	for _, path := range paths {
		fmt.Printf("\n conf path (%s)\n", path)
		mi := service.ParseService(path)
		waiter.Add(1)

		go func() {
			defer waiter.Done()
			now := time.Now()
			if mi == nil || !mi.IsOK() {
				fmt.Printf("\nserver(%s) is invalid\n", mi.IP)
				return
			}

			mi.Ping = time.Now().Sub(now)
			fmt.Printf("\nserver(%s) is ok (%dms)\n", mi.IP, mi.Ping/time.Millisecond)
			locker.Lock()
			s = append(s, mi)
			locker.Unlock()
		}()
	}

	waiter.Wait()

	if len(s) == 0 {
		return nil
	}

	sort.Slice(s, func(i, j int) bool {
		return s[i].Ping < s[j].Ping
	})
	return s[0]
}
