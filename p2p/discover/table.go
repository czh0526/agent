package discover

import (
	"errors"
	"fmt"
	mrand "math/rand"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/czh0526/agent/common"
	"github.com/czh0526/agent/crypto"
)

const (
	bucketSize        = 16
	hashBits          = len(common.Hash{}) * 8
	nBuckets          = hashBits / 15
	bucketMinDistance = hashBits - nBuckets
	maxReplacements   = 10
)

type nodesByDistance struct {
	entries []*Node
	target  common.Hash
}

func (h *nodesByDistance) push(n *Node, maxElems int) {
	ix := sort.Search(len(h.entries), func(i int) bool {
		// a -> target > b -> target : true
		return distcmp(h.target, h.entries[i].sha, n.sha) > 0
	})
	if len(h.entries) < maxElems {
		h.entries = append(h.entries, n)
	}
	if ix == len(h.entries) {

	} else {
		// ix 及之后元素后移一位
		copy(h.entries[ix+1:], h.entries[ix:])
		// ix 位置插入 n
		h.entries[ix] = n
	}
}

type bucket struct {
	entries      []*Node
	replacements []*Node
}

// 判断 n 是否存在于 bucket; 如果存在，将其冒泡
func (b *bucket) bump(n *Node) bool {
	for i := range b.entries {
		if b.entries[i].ID == n.ID {
			copy(b.entries[1:], b.entries[:i])
			b.entries[0] = n
			return true
		}
	}
	return false
}

func (tab *Table) bumpOrAdd(b *bucket, n *Node) bool {
	// bucket.entries 中已经存在 n，冒泡
	if b.bump(n) {
		return true
	}

	// bucket.entries 中已经满了, 不能插入 n
	if len(b.entries) >= bucketSize {
		return false
	}

	// bucket.entries 中还没有满，n 也不在 bucket.entries 中
	b.entries, _ = pushNode(b.entries, n, bucketSize)
	b.replacements = deleteNode(b.replacements, n)
	n.addedAt = time.Now()
	return true
}

func (tab *Table) addReplacement(b *bucket, n *Node) {
	for _, e := range b.replacements {
		if e.ID == n.ID {
			return
		}
	}

	b.replacements, _ = pushNode(b.replacements, n, maxReplacements)
}

// 将节点 n 压入 list 头部
func pushNode(list []*Node, n *Node, max int) ([]*Node, *Node) {
	if len(list) < max {
		list = append(list, nil)
	}
	removed := list[len(list)-1]
	copy(list[1:], list)
	list[0] = n
	return list, removed
}

func deleteNode(list []*Node, n *Node) []*Node {
	for i := range list {
		if list[i].ID == n.ID {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

type bondproc struct {
	err  error
	n    *Node
	done chan struct{}
}

type transport interface {
	ping(NodeID, *net.UDPAddr) error
	waitping(NodeID) error
	findnode(toid NodeID, addr *net.UDPAddr, target NodeID) ([]*Node, error)
	close()
}

type Table struct {
	mutex   sync.Mutex
	buckets [nBuckets]*bucket
	nursery []*Node
	rand    *mrand.Rand

	refreshReq chan chan struct{}
	initDone   chan struct{}
	closeReq   chan struct{}
	closed     chan struct{}

	bondmu    sync.Mutex
	bonding   map[NodeID]*bondproc
	bondslots chan struct{}

	net  transport
	self *Node
}

func newTable(t transport, ourID NodeID, ourAddr *net.UDPAddr, bootnodes []*Node) (*Table, error) {
	tab := &Table{
		net:        t,
		self:       NewNode(ourID, ourAddr.IP, uint16(ourAddr.Port), uint16(ourAddr.Port)),
		bonding:    make(map[NodeID]*bondproc),
		bondslots:  make(chan struct{}, 16),
		refreshReq: make(chan chan struct{}),
		initDone:   make(chan struct{}),
		closeReq:   make(chan struct{}),
		closed:     make(chan struct{}),
		rand:       mrand.New(mrand.NewSource(0)),
	}

	if err := tab.setFallbackNodes(bootnodes); err != nil {
		return nil, err
	}
	for i := 0; i < cap(tab.bondslots); i++ {
		tab.bondslots <- struct{}{}
	}

	for i := range tab.buckets {
		tab.buckets[i] = &bucket{}
	}

	go tab.loop()
	return tab, nil
}

func (tab *Table) setFallbackNodes(nodes []*Node) error {
	tab.nursery = make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		cpy := *n
		cpy.sha = crypto.Keccak256Hash(n.ID[:])
		tab.nursery = append(tab.nursery, &cpy)
	}
	return nil
}

func (tab *Table) Self() *Node {
	return tab.self
}

func (t *Table) Close() {
}

func (t *Table) Resolve(target NodeID) *Node {
	return nil
}

func (tab *Table) Lookup(targetID NodeID) []*Node {
	return tab.lookup(targetID, true)
}

func (tab *Table) lookup(targetID NodeID, refreshIfEmpty bool) []*Node {
	var (
		target = crypto.Keccak256Hash(targetID[:])
		asked  = make(map[NodeID]bool)
		//seen           = make(map[NodeID]bool)
		//reply          = make(chan []*Node, 3)
		//pendingQueries = 0
		result *nodesByDistance
	)

	asked[tab.self.ID] = true

	for {
		tab.mutex.Lock()
		result = tab.closest(target, bucketSize)
		tab.mutex.Unlock()
		if len(result.entries) > 0 || !refreshIfEmpty {
			break
		}

		// 没有得到想要的结果，刷新节点缓存
		<-tab.refresh()
		refreshIfEmpty = false
	}

	return nil
}

func (t *Table) ReadRandomNodes([]*Node) int {
	fmt.Println("discoveryTable.ReadRandomNodes() was called...")
	return 0
}

// 取出离 target 最近的 nresults 个结果
func (tab *Table) closest(target common.Hash, nresults int) *nodesByDistance {
	close := &nodesByDistance{target: target}
	for _, b := range tab.buckets {
		for _, n := range b.entries {
			close.push(n, nresults)
		}
	}
	return close
}

func (tab *Table) refresh() <-chan struct{} {
	done := make(chan struct{})
	select {
	case tab.refreshReq <- done:
	case <-tab.closed:
		close(done)
	}
	return done
}

func (tab *Table) loop() {
	var (
		revalidate = time.NewTimer(time.Second * 10)
		refresh    = time.NewTicker(time.Minute * 30)
		copyNodes  = time.NewTicker(time.Second * 30)
		//revalidateDone = make(chan struct{})
		// 正在执行的 doRefresh 如果结束，会通过 refreshDone 通知
		refreshDone = make(chan struct{})
		// refresh 操作的请求队列
		waiting = []chan struct{}{tab.initDone}
	)
	// 退出时，关闭定时器
	defer refresh.Stop()
	defer revalidate.Stop()
	defer copyNodes.Stop()

	go tab.doRefresh(refreshDone)

loop:
	for {
		select {
		case req := <-tab.refreshReq:
			// 将请求排队
			waiting = append(waiting, req)
			// 如果当前没有 doRefresh() 在执行，启动它，并设置结束信号灯
			if refreshDone == nil {
				refreshDone = make(chan struct{})
				go tab.doRefresh(refreshDone)
			}
		case <-refreshDone: // 检测 doRefresh() 结束
			// 消除排队的请求
			for _, ch := range waiting {
				close(ch)
			}
			// 清空队列，关闭信号灯
			waiting, refreshDone = nil, nil
		case <-tab.closeReq:
			break loop
		}
	}

	close(tab.closed)
}

func (tab *Table) doRefresh(done chan struct{}) {
	defer close(done)

	tab.loadSeedNodes(true)
	tab.lookup(tab.self.ID, false)
}

func (tab *Table) loadSeedNodes(bond bool) {
	// 将 bootnodes 加入 seeds
	seeds := make([]*Node, 0, 10)
	seeds = append(seeds, tab.nursery...)
	// 将不能被连接的节点去除
	if bond {
		seeds = tab.bondall(seeds)
	}
	// 加入节点表
	for i := range seeds {
		seed := seeds[i]
		tab.add(seed)
	}
}

func (tab *Table) add(new *Node) {
	tab.mutex.Lock()
	defer tab.mutex.Unlock()

	// 定位到 bucket
	b := tab.bucket(new.sha)
	if !tab.bumpOrAdd(b, new) {
		tab.addReplacement(b, new)
	}
}

func (tab *Table) bucket(sha common.Hash) *bucket {
	d := logdist(tab.self.sha, sha)
	if d <= bucketMinDistance {
		return tab.buckets[0]
	}
	return tab.buckets[d-bucketMinDistance-1]
}

func (tab *Table) isInitDone() bool {
	select {
	case <-tab.initDone:
		return true
	default:
		return false
	}
}

func (tab *Table) bondall(nodes []*Node) (result []*Node) {
	// 通讯管道
	rc := make(chan *Node, len(nodes))
	// 启动 bond 流程
	for i := range nodes {
		go func(n *Node) {
			nn, _ := tab.bond(false, n.ID, n.addr(), n.TCP)
			// 序列化返回值
			rc <- nn
		}(nodes[i])
	}
	for range nodes {
		if n := <-rc; n != nil {
			result = append(result, n)
		}
	}
	return result
}

/*
	pinged: 是否是被 ping 消息触发
*/
func (tab *Table) bond(pinged bool, id NodeID, addr *net.UDPAddr, tcpPort uint16) (*Node, error) {
	fmt.Printf("bond(pinged=%v) was called. \n", pinged)
	if id == tab.self.ID {
		return nil, nil
	}
	if pinged && !tab.isInitDone() {
		return nil, errors.New("still initializing")
	}

	var (
		node   *Node
		result error
	)
	tab.bondmu.Lock()
	w := tab.bonding[id]
	if w != nil {
		// 等待 bondproc 执行完成
		tab.bondmu.Unlock()
		<-w.done
	} else {
		// 设置任务
		w = &bondproc{done: make(chan struct{})}
		tab.bonding[id] = w
		tab.bondmu.Unlock()
		// 执行任务
		tab.pingpong(w, pinged, id, addr, tcpPort)
		// 删除任务
		tab.bondmu.Lock()
		delete(tab.bonding, id)
		tab.bondmu.Unlock()
	}

	result = w.err
	if result == nil {
		node = w.n
	}

	if node != nil {
		tab.add(node)
	}
	return node, result
}

func (tab *Table) pingpong(w *bondproc, pinged bool, id NodeID, addr *net.UDPAddr, tcpPort uint16) {
	// 获得令牌
	<-tab.bondslots
	// 归还令牌
	defer func() {
		tab.bondslots <- struct{}{}
	}()

	// 发送 ping 命令
	if w.err = tab.ping(id, addr); w.err != nil {
		close(w.done)
		return
	}

	// 如果是首发节点，等待被激活节点的 ping 命令
	if !pinged {
		tab.net.waitping(id)
	}

	// 构建 Node 对象
	w.n = NewNode(id, addr.IP, uint16(addr.Port), tcpPort)
	// 通知调用者，pingpong 过程完成
	close(w.done)
}

func (tab *Table) ping(id NodeID, addr *net.UDPAddr) error {
	if err := tab.net.ping(id, addr); err != nil {
		return err
	}
	return nil
}
