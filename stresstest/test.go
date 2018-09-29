package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sync"

	"github.com/iost-official/go-iost/account"
	"github.com/iost-official/go-iost/common"
	"github.com/iost-official/go-iost/core/tx"
	"github.com/iost-official/go-iost/crypto"
	pb "github.com/iost-official/go-iost/rpc"
	"google.golang.org/grpc"
)

var conns []*grpc.ClientConn

func initConn(num int) {
	conns = make([]*grpc.ClientConn, num)
	allServers := []string{"127.0.0.1:30002"} //, "18.228.149.97:30002"} //"13.237.151.211:30002", "35.177.202.166:30002", "18.136.110.166:30002", "13.232.76.188:30002", "52.59.86.255:30002"} //, "54.180.13.100:30002", "35.183.163.183:30002"}
	for i := 0; i < num; i++ {
		conn, err := grpc.Dial(allServers[i%len(allServers)], grpc.WithInsecure())
		if err != nil {
			continue
		}
		conns[i] = conn
	}
}

func transParallel(num int) {
	if conns == nil {
		initConn(num)
	}
	wg := new(sync.WaitGroup)
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(i int) {
			transfer(i)
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func loadBytes(s string) []byte {
	if s[len(s)-1] == 10 {
		s = s[:len(s)-1]
	}
	buf := common.Base58Decode(s)
	return buf
}

func sendTx(stx *tx.Tx, i int) ([]byte, error) {
	if conns[i] == nil {
		return nil, errors.New("nil conn")
	}
	client := pb.NewApisClient(conns[i])
	resp, err := client.SendRawTx(context.Background(), &pb.RawTxReq{Data: stx.Encode()})
	if err != nil {
		//conns[i], _ = grpc.Dial(allServers[i%len(allServers)], grpc.WithInsecure())
		return nil, err
	}
	return []byte(resp.Hash), nil
}

func transfer(i int) {
	action := tx.NewAction("iost.system", "Transfer", `["IOSTjBxx7sUJvmxrMiyjEQnz9h5bfNrXwLinkoL9YvWjnrGdbKnBP","IOSTgw6cmmWyiW25TMAK44N9coLCMaygx5eTfGVwjCcriEWEEjK2H",1]`)
	acc, _ := account.NewAccount(loadBytes("5ifJUpGWJ69S2eKsKYLDcajVxrc5yZk2CD7tJ29yK6FyjAtmeboK3G4Ag5p22uZTijBP3ftEDV4ymXZF1jGqu9j4"), crypto.Ed25519)
	trx := tx.NewTx([]*tx.Action{&action}, [][]byte{}, 1000, 1, time.Now().Add(time.Second*time.Duration(10000)).UnixNano())
	trx.Time = 0
	trx.Expiration = 0
	stx, err := tx.SignTx(trx, acc)
	if err != nil {
		fmt.Println("signtx", stx, err)
		return
	}
	var txHash []byte
	txHash, err = sendTx(stx, i)
	if err != nil {
		fmt.Println("sendtx", txHash, err)
		return
	}
}

func main() {
	var num = 500000
	for i := 0; i < num; i++ {
		transParallel(1)
	}
}
