package discover

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mrand "math/rand"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/czh0526/agent/log"

	"github.com/czh0526/agent/common"
	"github.com/czh0526/agent/crypto"
)

const (
	alpha           = 3 // lookup 过程中，findnode() 并行执行的数量
	maxReplacements = 10

	bucketSize        = 16
	hashBits          = len(common.Hash{}) * 8
	nBuckets          = hashBits / 15
	bucketMinDistance = hashBits - nBuckets

	maxFindnodeFailures = 5
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

	db         *nodeDB
	refreshReq chan chan struct{}
	initDone   chan struct{}
	closeReq   chan struct{}
	closed     chan struct{}

	bondmu    sync.Mutex
	bonding   map[NodeID]*bondproc // controling bond(), 避免对同一个 Node 同时进行多次握手
	bondslots chan struct{}        // controling pingpong(), 避免并行发起的握手过程超过16个

	net  transport
	self *Node
}

func newTable(t transport, ourID NodeID, ourAddr *net.UDPAddr, nodeDBPath string, bootnodes []*Node) (*Table, error) {
	db, err := newNodeDB(nodeDBPath, Version, ourID)
	if err != nil {
		return nil, err
	}
	tab := &Table{
		net:        t,
		db:         db,
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

	log.Info(" >> 启动 discoverTable 模块.")
	go tab.loop()
	log.Info(" << discoverTable 模块启动完成.")
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

/*
	根据现有的 K-bucket, 查找指定的 targetID
*/
func (tab *Table) Lookup(targetID NodeID) []*Node {
	return tab.lookup(targetID, true)
}

// 查找邻居节点的核心算法
func (tab *Table) lookup(targetID NodeID, refreshIfEmpty bool) []*Node {
	var (
		target         = crypto.Keccak256Hash(targetID[:])
		asked          = make(map[NodeID]bool) // 发送过 findnode 消息的节点集合
		seen           = make(map[NodeID]bool) // 发回响应的节点集合
		reply          = make(chan []*Node, alpha)
		pendingQueries = 0
		result         *nodesByDistance
	)

	asked[tab.self.ID] = true

	for {
		tab.mutex.Lock()
		// 获取矢量距离最近的节点
		result = tab.closest(target, bucketSize)
		tab.mutex.Unlock()
		log.Trace("[Table]: 获取 target 的邻居节点", "refreshIfEmpty", refreshIfEmpty, "target closest node", len(result.entries))
		if len(result.entries) > 0 || !refreshIfEmpty {
			break
		}

		// 没有取到结果，刷新节点缓存
		<-tab.refresh()
		refreshIfEmpty = false
	}

	for {
		// 针对每个 Node, 并发执行 findnode() 操作
		for i := 0; i < len(result.entries) && pendingQueries < alpha; i++ {
			n := result.entries[i]
			if !asked[n.ID] {
				asked[n.ID] = true
				pendingQueries++
				go func() {
					r, err := tab.net.findnode(n.ID, n.addr(), targetID)
					if err != nil {
						fails := tab.db.findFails(n.ID) + 1
						tab.db.updateFindFails(n.ID, fails)

						if fails >= maxFindnodeFailures {
							tab.delete(n)
						}
					}
					reply <- tab.bondall(r)
				}()
			}
		}

		if pendingQueries == 0 {
			break
		}

		// 保存一个 findnode() 返回例程的执行结果
		for _, n := range <-reply {
			if n != nil && !seen[n.ID] {
				seen[n.ID] = true
				result.push(n, bucketSize)
			}
		}
		// 并发例程数量减 1
		pendingQueries--
	}

	return result.entries
}

func (t *Table) ReadRandomNodes([]*Node) int {
	fmt.Println("discoveryTable.ReadRandomNodes() was called...")
	return 0
}

// 遍历k-bucket, 取出离 target 最近的 nresults 个节点
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
	log.Debug("[Table]: 请求刷新 K-bucket.")
	done := make(chan struct{})
	select {
	case tab.refreshReq <- done:
	case <-tab.closed:
		close(done)
	}
	// 返回 chan 变量，用于后续获取执行结果
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
		case <-refresh.C:
			tab.seedRand()
			if refreshDone == nil {
				refreshDone = make(chan struct{})
				go tab.doRefresh(refreshDone)
			}

		case req := <-tab.refreshReq:
			log.Debug("[Table]: 处理 K-bucket 的刷新请求", "refreshDone", refreshDone)
			waiting = append(waiting, req)
			// 当前没有 doRefresh() 在执行
			if refreshDone == nil {
				// 为新一轮的 doRefresh() 执行重新生成信号灯变量
				refreshDone = make(chan struct{})
				// 启动 doRefresh()
				log.Debug("[Table]: 启动 doRefresh() 例程")
				go tab.doRefresh(refreshDone)
			}
		case <-refreshDone: // 检测 doRefresh() 结束
			log.Debug("[Table]: K-bucket 的刷新结束.")
			// 批量设置排队请求的 chan 变量
			for _, ch := range waiting {
				close(ch)
			}
			// 清空队列，结束信号灯变量
			waiting, refreshDone = nil, nil
		case <-tab.closeReq:
			break loop
		}
	}

	// 关闭 udp 端口
	if tab.net != nil {
		tab.net.close()
	}

	// 等待最后一次 doRefresh()
	if refreshDone != nil {
		<-refreshDone
	}
	for _, ch := range waiting {
		close(ch)
	}

	// 关闭数据库
	tab.db.close()
	// 关闭 tab 对象
	close(tab.closed)
}

/*
	通过 seed nodes, 找自己的邻居节点
*/
func (tab *Table) doRefresh(done chan struct{}) {
	defer close(done)

	log.Trace("[Table]: 填充 seed nodes.")
	tab.loadSeedNodes(true)
	log.Trace("[Table]: 基于 seed nodes，通过 lookup(), 找自己的邻居节点.")
	tab.lookup(tab.self.ID, false)
}

func (tab *Table) seedRand() {
	var b [8]byte
	crand.Read(b[:])

	tab.mutex.Lock()
	tab.rand.Seed(int64(binary.BigEndian.Uint64(b[:])))
	tab.mutex.Unlock()
}

// 检测种子节点的有效性，并将种子节点加入 Table
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
		log.Debug("[Table]: 加入一个 seed node.", "Node IP", seed.IP)
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

func (tab *Table) delete(node *Node) {
	tab.mutex.Lock()
	defer tab.mutex.Unlock()

	tab.deleteInBucket(tab.bucket(node.sha), node)
}

func (tab *Table) deleteInBucket(b *bucket, n *Node) {
	b.entries = deleteNode(b.entries, n)
}

// 根据 node.sha 定位 bucket
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

// 异步方式实现的同步函数
func (tab *Table) bondall(nodes []*Node) (result []*Node) {
	// 通讯管道
	rc := make(chan *Node, len(nodes))
	// 并发启动 bond 流程
	for i := range nodes {
		go func(n *Node) {
			log.Trace("启动 bond 过程", "target node", n.IP)
			nn, _ := tab.bond(false, n.ID, n.addr(), n.TCP)
			// 序列化返回值
			rc <- nn
		}(nodes[i])
	}

	// 同步等待，获取并发握手的结果
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
	if id == tab.self.ID {
		return nil, nil
	}

	if pinged {
		log.Trace("[UDP]: 进入 bond 过程 --> 响应 ping 消息 ...")
	} else {
		log.Trace("[UDP]: 进入 bond 过程 --> 发起 ping 消息 ... ")
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
		log.Debug("[Table]: 加入一个 node.", "Node IP", node.IP)
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
	tab.db.updateLastPing(id, time.Now())
	if err := tab.net.ping(id, addr); err != nil {
		return err
	}
	tab.db.updateBondTime(id, time.Now())
	return nil
}
