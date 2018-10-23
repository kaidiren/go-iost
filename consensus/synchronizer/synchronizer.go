package synchronizer

import (
	"sort"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/iost-official/go-iost/core/block"
	"github.com/iost-official/go-iost/core/blockcache"
	"github.com/iost-official/go-iost/core/global"
	"github.com/iost-official/go-iost/core/message"
	"github.com/iost-official/go-iost/ilog"
	"github.com/iost-official/go-iost/p2p"
)

var (
	// TODO: configurable
	confirmNumber           int64 = 8
	maxBlockHashQueryNumber int64 = 100
	retryTime                     = 5 * time.Second
	checkTime                     = 3 * time.Second
	syncHeightTime                = 3 * time.Second
	heightAvailableTime     int64 = 22 * 3
	heightTimeout           int64 = 100 * 22 * 3
	continuousNum           int64 = 5
	syncNumber              int64 = 11 * continuousNum
)

// Synchronizer defines the functions of synchronizer module
type Synchronizer interface {
	Start() error
	Stop()
	CheckSync() bool
	CheckGenBlock(hash []byte) bool
	CheckSyncProcess()
}

//SyncImpl is the implementation of Synchronizer.
type SyncImpl struct {
	p2pService   p2p.Service
	blockCache   blockcache.BlockCache
	lastBcn      *blockcache.BlockCacheNode
	basevariable global.BaseVariable
	dc           DownloadController
	reqMap       *sync.Map
	heightMap    *sync.Map
	syncEnd      int64

	messageChan    chan p2p.IncomingMessage
	syncHeightChan chan p2p.IncomingMessage
	exitSignal     chan struct{}
}

// NewSynchronizer returns a SyncImpl instance.
func NewSynchronizer(basevariable global.BaseVariable, blkcache blockcache.BlockCache, p2pserv p2p.Service) (*SyncImpl, error) {
	sy := &SyncImpl{
		p2pService:   p2pserv,
		blockCache:   blkcache,
		basevariable: basevariable,
		reqMap:       new(sync.Map),
		heightMap:    new(sync.Map),
		lastBcn:      nil,
		syncEnd:      0,
	}
	var err error
	sy.dc, err = NewDownloadController()
	if err != nil {
		return nil, err
	}

	sy.messageChan = sy.p2pService.Register("sync message",
		p2p.SyncBlockRequest,
		p2p.SyncBlockHashRequest,
		p2p.SyncBlockHashResponse,
	)

	sy.syncHeightChan = sy.p2pService.Register("sync height", p2p.SyncHeight)
	sy.exitSignal = make(chan struct{})

	return sy, nil
}

// Start starts the synchronizer module.
func (sy *SyncImpl) Start() error {
	go sy.dc.FreePeerLoop(sy.checkHasBlock)
	go sy.dc.DownloadLoop(sy.reqSyncBlock)
	go sy.syncHeightLoop()
	go sy.messageLoop()
	go sy.retryDownloadLoop()
	go sy.initializer()
	return nil
}

// Stop stops the synchronizer module.
func (sy *SyncImpl) Stop() {
	sy.dc.Stop()
	close(sy.exitSignal)
}

func (sy *SyncImpl) initializer() {
	if sy.basevariable.Mode() != global.ModeInit {
		return
	}
	for {
		select {
		case <-time.After(retryTime):
			if sy.basevariable.BlockChain().Length() == 0 {
				ilog.Errorf("block chain is empty")
				return
			}
			sy.basevariable.SetMode(global.ModeNormal)
			//sy.checkSync()
			return
		case <-sy.exitSignal:
			return
		}
	}
}

func (sy *SyncImpl) syncHeightLoop() {
	syncHeightTicker := time.NewTicker(syncHeightTime)
	checkTicker := time.NewTicker(checkTime)
	for {
		select {
		case <-syncHeightTicker.C:
			//num := sy.blockCache.Head().Number
			//sh := &message.SyncHeight{Height: num, Time: time.Now().Unix()}
			//bytes, err := proto.Marshal(sh)
			//if err != nil {
			//	ilog.Errorf("marshal syncheight failed. err=%v", err)
			//	continue
			//}
			//ilog.Infof("broadcast sync height")
			//sy.p2pService.Broadcast(bytes, p2p.SyncHeight, p2p.UrgentMessage)
		case req := <-sy.syncHeightChan:
			var sh message.SyncHeight
			err := proto.Unmarshal(req.Data(), &sh)
			if err != nil {
				ilog.Errorf("unmarshal syncheight failed. err=%v", err)
				continue
			}
			if shIF, ok := sy.heightMap.Load(req.From()); ok {
				if shOld, ok := shIF.(*message.SyncHeight); ok {
					if shOld.Height == sh.Height {
						continue
					}
				}
			}
			ilog.Infof("sync height from: %s, height: %v, time:%v", req.From().Pretty(), sh.Height, sh.Time)
			sy.heightMap.Store(req.From(), &sh)
		case <-checkTicker.C:
			//sy.checkSync()
			//sy.checkGenBlock()
			//sy.CheckSyncProcess()
		case <-sy.exitSignal:
			syncHeightTicker.Stop()
			checkTicker.Stop()
			return
		}
	}
}

func (sy *SyncImpl) checkSync() bool {
	if sy.basevariable.Mode() != global.ModeNormal {
		return false
	}
	height := sy.basevariable.BlockChain().Length() - 1
	heights := make([]int64, 0, 0)
	heights = append(heights, sy.blockCache.Head().Number)
	now := time.Now().Unix()
	sy.heightMap.Range(func(k, v interface{}) bool {
		sh, ok := v.(*message.SyncHeight)
		if !ok || sh.Time+heightAvailableTime < now {
			if sh.Time+heightTimeout < now {
				sy.heightMap.Delete(k)
			}
			return true
		}
		heights = append(heights, 0)
		r := len(heights) - 1
		for 0 < r && heights[r-1] > sh.Height {
			heights[r] = heights[r-1]
			r--
		}
		heights[r] = sh.Height
		return true
	})
	netHeight := heights[len(heights)/2]
	ilog.Infof("check sync, heights: %+v", heights)
	if netHeight > height+syncNumber {
		sy.basevariable.SetMode(global.ModeSync)
		sy.dc.Reset()
		go sy.syncBlocks(height+1, netHeight)
		return true
	}
	return false
}

func (sy *SyncImpl) checkGenBlock() bool {
	if sy.basevariable.Mode() != global.ModeNormal {
		return false
	}
	bcn := sy.blockCache.Head()
	for bcn != nil && bcn.Block.Head.Witness == sy.basevariable.Config().ACC.ID {
		bcn = bcn.Parent
	}
	if bcn == nil {
		return false
	}
	height := sy.basevariable.BlockChain().Length() - 1
	var num int64
	if bcn != sy.lastBcn {
		sy.lastBcn = bcn
		witness := bcn.Block.Head.Witness
		for i := int64(0); i < confirmNumber*continuousNum; i++ {
			if bcn == nil {
				break
			}
			if witness == bcn.Block.Head.Witness {
				num++
			}
			bcn = bcn.Parent
		}
	}
	if num > continuousNum {
		ilog.Infof("num: %v, continuousNum: %v", num, continuousNum)
		go sy.syncBlocks(height+1, sy.blockCache.Head().Number)
		return true
	}
	return false
}

func (sy *SyncImpl) queryBlockHash(hr *message.BlockHashQuery) {
	bytes, err := hr.Marshal()
	if err != nil {
		ilog.Errorf("marshal blockhashquery failed. err=%v", err)
		return
	}
	ilog.Infof("[sync] request block hash. reqtype=%v, start=%v, end=%v, nums size=%v", hr.ReqType, hr.Start, hr.End, len(hr.Nums))
	sy.p2pService.Broadcast(bytes, p2p.SyncBlockHashRequest, p2p.UrgentMessage)
}

func (sy *SyncImpl) syncBlocks(startNumber int64, endNumber int64) error {
	ilog.Infof("sync Blocks %v, %v", startNumber, endNumber)
	sy.syncEnd = endNumber
	for endNumber > startNumber+maxBlockHashQueryNumber-1 {
		for sy.blockCache.Head().Number+3 < startNumber {
			time.Sleep(500 * time.Millisecond)
		}
		for i := startNumber; i < startNumber+maxBlockHashQueryNumber; i++ {
			sy.reqMap.Store(i, true)
		}
		sy.queryBlockHash(&message.BlockHashQuery{ReqType: 0, Start: startNumber, End: startNumber + maxBlockHashQueryNumber - 1, Nums: nil})
		startNumber += maxBlockHashQueryNumber
	}
	if startNumber <= endNumber {
		for i := startNumber; i <= endNumber; i++ {
			sy.reqMap.Store(i, true)
		}
		sy.queryBlockHash(&message.BlockHashQuery{ReqType: 0, Start: startNumber, End: endNumber, Nums: nil})
	}
	return nil
}

// CheckSyncProcess checks if the end of sync.
func (sy *SyncImpl) CheckSyncProcess() {
	ilog.Infof("check sync process: now %v, end %v", sy.blockCache.Head().Number, sy.syncEnd)
	if sy.syncEnd <= sy.blockCache.Head().Number {
		sy.basevariable.SetMode(global.ModeNormal)
		sy.dc.Reset()
	}
}

func (sy *SyncImpl) messageLoop() {
	for {
		select {
		case req := <-sy.messageChan:
			switch req.Type() {
			case p2p.SyncBlockHashRequest:
				var rh message.BlockHashQuery
				err := rh.Unmarshal(req.Data())
				if err != nil {
					ilog.Errorf("unmarshal BlockHashQuery failed:%v", err)
					break
				}
				go sy.handleHashQuery(&rh, req.From())
			case p2p.SyncBlockHashResponse:
				var rh message.BlockHashResponse
				err := rh.Unmarshal(req.Data())
				if err != nil {
					ilog.Errorf("unmarshal BlockHashResponse failed:%v", err)
					break
				}
				go sy.handleHashResp(&rh, req.From())
			case p2p.SyncBlockRequest:
				var rh message.BlockInfo
				err := rh.Unmarshal(req.Data())
				if err != nil {
					break
				}
				go sy.handleBlockQuery(&rh, req.From())
			}
		case <-sy.exitSignal:
			return
		}
	}
}

func (sy *SyncImpl) getBlockHashes(start int64, end int64) *message.BlockHashResponse {
	resp := &message.BlockHashResponse{
		BlockInfos: make([]*message.BlockInfo, 0, end-start+1),
	}
	node := sy.blockCache.Head()
	if node != nil && end > node.Number {
		end = node.Number
	}

	for i := end; i >= start; i-- {
		var hash []byte
		var err error

		for node != nil && i < node.Number {
			node = node.Parent
		}

		if node != nil {
			hash = node.Block.HeadHash()
		} else {
			hash, err = sy.basevariable.BlockChain().GetHashByNumber(i)
			if err != nil {
				ilog.Errorf("get hash by number from db failed. err=%v, number=%v", err, i)
				continue
			}
		}

		blkInfo := message.BlockInfo{
			Number: i,
			Hash:   hash,
		}
		resp.BlockInfos = append(resp.BlockInfos, &blkInfo)
	}
	for i, j := 0, len(resp.BlockInfos)-1; i < j; i, j = i+1, j-1 {
		resp.BlockInfos[i], resp.BlockInfos[j] = resp.BlockInfos[j], resp.BlockInfos[i]
	}
	return resp
}

func (sy *SyncImpl) getBlockHashesByNums(nums []int64) *message.BlockHashResponse {
	resp := &message.BlockHashResponse{
		BlockInfos: make([]*message.BlockInfo, 0, len(nums)),
	}
	var blk *block.Block
	var err error
	for _, num := range nums {
		var hash []byte
		blk, err = sy.blockCache.GetBlockByNumber(num)
		if err == nil {
			hash = blk.HeadHash()
		} else {
			hash, err = sy.basevariable.BlockChain().GetHashByNumber(num)
			if err != nil {
				continue
			}
		}
		blkInfo := message.BlockInfo{
			Number: num,
			Hash:   hash,
		}
		resp.BlockInfos = append(resp.BlockInfos, &blkInfo)
	}
	return resp
}

func (sy *SyncImpl) handleHashQuery(rh *message.BlockHashQuery, peerID p2p.PeerID) {
	if rh.End < rh.Start || rh.Start < 0 {
		return
	}
	var resp *message.BlockHashResponse

	switch rh.ReqType {
	case message.RequireType_GETBLOCKHASHES:
		resp = sy.getBlockHashes(rh.Start, rh.End)
	case message.RequireType_GETBLOCKHASHESBYNUMBER:
		resp = sy.getBlockHashesByNums(rh.Nums)
	}

	if len(resp.BlockInfos) == 0 {
		return
	}
	bytes, err := resp.Marshal()
	if err != nil {
		ilog.Errorf("marshal BlockHashResponse failed:struct=%v, err=%v", resp, err)
		return
	}
	sy.p2pService.SendToPeer(peerID, bytes, p2p.SyncBlockHashResponse, p2p.NormalMessage)
}

func (sy *SyncImpl) handleHashResp(rh *message.BlockHashResponse, peerID p2p.PeerID) {
	ilog.Infof("receive block hashes: len=%v", len(rh.BlockInfos))
	for _, blkInfo := range rh.BlockInfos {
		if blkInfo.Number > sy.blockCache.LinkedRoot().Number {
			if _, err := sy.blockCache.Find(blkInfo.Hash); err != nil {
				sy.dc.CreateMission(string(blkInfo.Hash), blkInfo.Number, peerID)
			}
		}
		sy.reqMap.Delete(blkInfo.Number)
	}
}

func (sy *SyncImpl) retryDownloadLoop() {
	for {
		select {
		case <-time.After(retryTime):
			hq := &message.BlockHashQuery{ReqType: 1, Start: 0, End: 0, Nums: make([]int64, 0)}
			sy.reqMap.Range(func(k, v interface{}) bool {
				num, ok := k.(int64)
				if !ok {
					sy.reqMap.Delete(k)
					return true
				}
				hq.Nums = append(hq.Nums, num)
				return true
			})
			if len(hq.Nums) > 0 {
				ilog.Infof("retry download ", hq.Nums)
				sort.Slice(hq.Nums, func(i int, j int) bool {
					return hq.Nums[i] < hq.Nums[j]
				})
				sy.queryBlockHash(hq)
			}
		case <-sy.exitSignal:
			return
		}
	}
}

func (sy *SyncImpl) handleBlockQuery(rh *message.BlockInfo, peerID p2p.PeerID) {
	var b []byte
	var err error
	node, err := sy.blockCache.Find(rh.Hash)
	if err == nil {
		b, err = node.Block.Encode()
		if err != nil {
			ilog.Errorf("Fail to encode block: %v, err=%v", rh.Number, err)
			return
		}
		sy.p2pService.SendToPeer(peerID, b, p2p.SyncBlockResponse, p2p.NormalMessage)
		return
	}
	b, err = sy.basevariable.BlockChain().GetBlockByteByHash(rh.Hash)
	if err != nil {
		ilog.Errorf("handle block query failed to get block.")
		return
	}
	sy.p2pService.SendToPeer(peerID, b, p2p.SyncBlockResponse, p2p.NormalMessage)
}

func (sy *SyncImpl) checkHasBlock(hash string, p interface{}) bool {
	bn, ok := p.(int64)
	if !ok {
		ilog.Errorf("get p failed.")
		return false
	}
	if bn <= sy.blockCache.LinkedRoot().Number {
		return true
	}
	bHash := []byte(hash)
	if _, err := sy.blockCache.Find(bHash); err == nil {
		return true
	}
	return false
}

func (sy *SyncImpl) reqSyncBlock(hash string, p interface{}, peerID interface{}) (bool, bool) {
	bn, ok := p.(int64)
	if !ok {
		ilog.Errorf("get p failed.")
		return false, false
	}
	ilog.Infof("callback try sync block, num:%v", bn)
	if bn <= sy.blockCache.LinkedRoot().Number {
		ilog.Infof("callback block confirmed, num:%v", bn)
		return false, true
	}
	bHash := []byte(hash)
	if bcn, err := sy.blockCache.Find(bHash); err == nil {
		if bcn.Type == blockcache.Linked {
			ilog.Infof("callback block linked, num:%v", bn)
			return false, true
		}
		ilog.Infof("callback block is a single block, num:%v", bn)
		return false, false
	}
	bi := message.BlockInfo{Number: bn, Hash: bHash}
	bytes, err := bi.Marshal()
	if err != nil {
		ilog.Errorf("marshal request block failed. err=%v", err)
		return false, false
	}
	pid, ok := peerID.(p2p.PeerID)
	if !ok {
		return false, false
	}
	sy.p2pService.SendToPeer(pid, bytes, p2p.SyncBlockRequest, p2p.UrgentMessage)
	return true, false
}
