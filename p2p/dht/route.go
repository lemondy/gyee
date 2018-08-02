/*
 *  Copyright (C) 2017 gyee authors
 *
 *  This file is part of the gyee library.
 *
 *  the gyee library is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  the gyee library is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with the gyee library.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package dht

import (
	"time"
	"crypto/rand"
	"container/list"
	"crypto/sha256"
	sch	"github.com/yeeco/gyee/p2p/scheduler"
	log	"github.com/yeeco/gyee/p2p/logger"
	config "github.com/yeeco/gyee/p2p/config"
)

//
// Constants
//
const (
	RutMgrName = sch.DhtRutMgrName		// Route manager name registered in scheduler
	rutMgrMaxNearest = 32				// Max nearest peers can be retrieved for a time
	rutMgrBucketSize = 32				// bucket size
	HashByteLength = 32					// 32 bytes(256 bits) hash applied
	HashBitLength = HashByteLength * 8	// hash bits
	rutMgrMaxLatency = time.Second * 60	// max latency in metric
	rutMgrMaxNofifee = 64				// max notifees could be
)

//
// Hash type
//
type Hash [HashByteLength]byte

//
// Latency measurement
//
type rutMgrPeerMetric struct {
	peerId		config.NodeID			// peer identity
	ltnSamples	[]time.Duration			// latency samples
	ewma		time.Duration			// exponentially-weighted moving avg
}

//
// Node in bucket
//
type rutMgrBucketNode struct {
	node	config.Node					// common node
	hash	Hash						// hash from node.ID
	dist	int							// distance between this node and local
}

//
// Route table
//
type rutMgrRouteTable struct {
	shaLocal		Hash								// local node identity hash
	bucketSize		int									// max peers can be held in one list
	bucketTab		[]*list.List						// buckets
	metricTab		map[config.NodeID]*rutMgrPeerMetric	// metric table about peers
	maxLatency		time.Duration						// max latency
}

//
// Notifee
//

type rutMgrNotifeeId struct {
	task			interface{}			// destionation task
	target			config.NodeID		// target aimed at
}

type rutMgrNotifee struct {
	id				rutMgrNotifeeId		// notifee identity
	max				int					// max nearest asked for
	nearests		[]*rutMgrBucketNode	// nearest peers
	dists			[]int				// distances of nearest peers
}

//
// Route manager
//
type RutMgr struct {
	sdl				*sch.Scheduler							// pointer to scheduler
	name			string									// my name
	tep				sch.SchUserTaskEp						// task entry
	ptnMe			interface{}								// pointer to task node of myself
	ptnQryMgr		interface{}								// pointer to query manager task node
	bpCfg			bootstrapPolicy							// bootstrap policy configuration
	bpTid			int										// bootstrap timer identity
	distLookupTab	[]int									// log2 distance lookup table for a xor byte
	localNodeId		config.NodeID							// local node identity
	rutTab			rutMgrRouteTable						// route table
	ntfTab			map[rutMgrNotifeeId]*rutMgrNotifee		// notifee table
}

//
// Bootstrap policy configuration
//
type bootstrapPolicy struct {
	randomQryNum	int					// times to try query for a random peer identity
	period			time.Duration		// timer period to fire a bootstrap
}

var defautBspCfg = bootstrapPolicy {
	randomQryNum:	2,
	period:			time.Minute * 1,
}

//
// Create route manager
//
func NewRutMgr() *RutMgr {

	rutMgr := RutMgr{
		sdl:			nil,
		name:			RutMgrName,
		tep:			nil,
		ptnMe:			nil,
		ptnQryMgr:		nil,
		bpCfg:			defautBspCfg,
		bpTid:			sch.SchInvalidTid,
		distLookupTab:	[]int{},
		localNodeId:	config.NodeID{},
		rutTab:			rutMgrRouteTable{},
		ntfTab:			make(map[rutMgrNotifeeId]*rutMgrNotifee, 0),
	}

	rutMgr.tep = rutMgr.rutMgrProc

	return &rutMgr
}

//
// Entry point exported to shceduler
//
func (rutMgr *RutMgr)TaskProc4Scheduler(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {
	return rutMgr.tep(ptn, msg)
}

//
// Route manager entry
//
func (rutMgr *RutMgr)rutMgrProc(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {

	eno := sch.SchEnoUnknown

	switch msg.Id {

	case sch.EvSchPoweron:
		eno = rutMgr.poweron(ptn)

	case sch.EvSchPoweroff:
		eno = rutMgr.poweroff(ptn)

	case sch.EvDhtRutBootstrapTimer:
		eno = rutMgr.bootstarpTimerHandler()

	case sch.EvDhtRutMgrNearestReq:
		sender := rutMgr.sdl.SchGetSender(msg)
		eno = rutMgr.nearestReq(sender, msg.Body.(*sch.MsgDhtRutMgrNearestReq))

	case sch.EvDhtRutMgrUpdateReq:
		eno = rutMgr.updateReq(msg.Body.(*sch.MsgDhtRutMgrUpdateReq))

	default:
		log.LogCallerFileLine("rutMgrProc: unknown message: %d", msg.Id)
		eno = sch.SchEnoParameter
	}

	return eno
}

//
// Poweron signal handler
//
func (rutMgr *RutMgr)poweron(ptn interface{}) sch.SchErrno {

	if ptn == nil {
		log.LogCallerFileLine("poweron: nil task node pointer")
		return sch.SchEnoParameter
	}

	var eno sch.SchErrno

	rutMgr.ptnMe = ptn
	rutMgr.sdl = sch.SchGetScheduler(ptn)
	eno, rutMgr.ptnQryMgr = rutMgr.sdl.SchGetTaskNodeByName(QryMgrName)
	
	if eno != sch.SchEnoNone || rutMgr.ptnQryMgr == nil {
		log.LogCallerFileLine("poweron: nil task node pointer")
		return eno
	}

	if dhtEno := rutMgr.getRouteConfig(); dhtEno != DhtEnoNone {
		log.LogCallerFileLine("poweron: getRouteConfig failed, dhtEno: %d", dhtEno)
		return sch.SchEnoUserTask
	}

	if dhtEno := rutMgrSetupLog2DistLKT(rutMgr.distLookupTab); dhtEno != DhtEnoNone {
		log.LogCallerFileLine("poweron: rutMgrSetupLog2DistLKT failed, dhtEno: %d", dhtEno)
		return sch.SchEnoUserTask
	}

	if dhtEno := rutMgr.rutMgrSetupRouteTable(); dhtEno != DhtEnoNone {
		log.LogCallerFileLine("poweron: rutMgrSetupRouteTable failed, dhtEno: %d", dhtEno)
		return sch.SchEnoUserTask
	}

	if dhtEno := rutMgr.startBspTimer(); dhtEno != DhtEnoNone {
		log.LogCallerFileLine("poweron: startBspTimer failed, dhtEno: %d", dhtEno)
		return sch.SchEnoUserTask
	}

	return sch.SchEnoNone
}

//
// Poweroff signal handler
//
func (rutMgr *RutMgr)poweroff(ptn interface{}) sch.SchErrno {
	log.LogCallerFileLine("poweroff: task will be done")
	return rutMgr.sdl.SchTaskDone(ptn, sch.SchEnoKilled)
}

//
// Bootstrap timer expired event handler
//
func (rutMgr *RutMgr)bootstarpTimerHandler() sch.SchErrno {

	sdl := rutMgr.sdl

	for loop := 0; loop < rutMgr.bpCfg.randomQryNum; loop++ {

		var msg = sch.SchMessage{}
		var req = sch.MsgDhtQryMgrQueryStartReq {
			Target: rutMgrRandomPeerId(),
		}

		sdl.SchMakeMessage(&msg, rutMgr.ptnMe, rutMgr.ptnQryMgr, sch.EvDhtQryMgrQueryStartReq, &req)
		sdl.SchSendMessage(&msg)
	}

	return sch.SchEnoNone
}

//
// Nearest peer request handler
//
func (rutMgr *RutMgr)nearestReq(tskSender interface{}, req *sch.MsgDhtRutMgrNearestReq) sch.SchErrno {

	if tskSender == nil || req == nil {
		log.LogCallerFileLine("nearestReq: " +
			"invalid parameters, tskSender: %p, req: %p",
			tskSender, req)
		return sch.SchEnoParameter
	}

	dhtEno, nearest, nearestDist := rutMgr.rutMgrNearest(&req.Target, req.Max)
	if dhtEno != DhtEnoNone {
		log.LogCallerFileLine("nearestReq: rutMgrNearest failed, eno: %d", dhtEno)
	}

	var rsp = sch.MsgDhtRutMgrNearestRsp {
		Eno:	int(dhtEno),
		Target:	req.Target,
		Peers:	nil,
		Dists:	nil,
	}
	var schMsg sch.SchMessage

	if dhtEno == DhtEnoNone && len(nearest) > 0 {
		rsp.Peers = nearest
		rsp.Dists = nearestDist
	}

	rutMgr.sdl.SchMakeMessage(&schMsg, rutMgr.ptnMe, tskSender, sch.EvDhtRutMgrNearestRsp, &rsp)
	rutMgr.sdl.SchSendMessage(&schMsg)

	if dhtEno != DhtEnoNone  {
		return sch.SchEnoUserTask
	}

	return sch.SchEnoNone
}

//
// Update route table request handler
//
func (rutMgr *RutMgr)updateReq(req *sch.MsgDhtRutMgrUpdateReq) sch.SchErrno {

	if req == nil || len(req.Seens) != len(req.Duras) || len(req.Seens) == 0 {
		log.LogCallerFileLine("updateReq: invalid prameter")
		return sch.SchEnoUserTask
	}

	rt := &rutMgr.rutTab

	for idx, n := range req.Seens {

		hash := rutMgrNodeId2Hash(n.ID)
		dist := rutMgr.rutMgrLog2Dist(&rt.shaLocal, hash)
		dur := req.Duras[idx]

		rt.rutMgrMetricSample(n.ID, dur)

		bn := rutMgrBucketNode {
			node:	n,
			hash:	*hash,
			dist:	dist,
		}

		rt.update(&bn, dist)
	}

	if dhtEno := rutMgr.rutMgrNotify(); dhtEno != DhtEnoNone {
		log.LogCallerFileLine("updateReq: rutMgrNotify failed, eno: %d", dhtEno)
		return sch.SchEnoUserTask
	}

	return sch.SchEnoNone
}

//
// Get route manager configuration
//
func (rutMgr *RutMgr)getRouteConfig() DhtErrno {

	rutCfg := config.P2pConfig4DhtRouteManager(RutMgrName)
	rutMgr.localNodeId = rutCfg.NodeId
	rutMgr.bpCfg.randomQryNum = rutCfg.RandomQryNum
	rutMgr.bpCfg.period = rutCfg.Period

	return DhtEnoNone
}

//
// Start bootstrap timer
//
func (rutMgr *RutMgr)startBspTimer() DhtErrno {

	var td = sch.TimerDescription {
		Name:	"dhtRutBspTimer",
		Utid:	sch.DhtRutBootstrapTimerId,
		Tmt:	sch.SchTmTypePeriod,
		Dur:	rutMgr.bpCfg.period,
		Extra:	nil,
	}

	if eno, tid := rutMgr.sdl.SchSetTimer(rutMgr.ptnMe, &td);
	eno != sch.SchEnoNone || tid == sch.SchInvalidTid {

		log.LogCallerFileLine("startBspTimer: " +
			"SchSetTimer failed, eno: %d, tid: %d",
			eno, tid)

		return DhtEnoScheduler
	}

	return DhtEnoNone
}

//
// Stop bootstrap timer
//
func (rutMgr *RutMgr)stopBspTimer() DhtErrno {

	var dhtEno DhtErrno = DhtEnoNone

	if rutMgr.bpTid != sch.SchInvalidTid {
		if eno := rutMgr.sdl.SchKillTimer(rutMgr.ptnMe, rutMgr.bpTid); eno != sch.SchEnoNone {
			dhtEno = DhtEnoScheduler
		}
	}
	rutMgr.bpTid = sch.SchInvalidTid

	return dhtEno
}

//
// Build random node identity
//
func rutMgrRandomPeerId() config.NodeID {
	var nid config.NodeID
	rand.Read(nid[:])
	return nid
}

//
// Build hash from node identity
//
func rutMgrRandomHashPeerId() *Hash {
	return rutMgrNodeId2Hash(rutMgrRandomPeerId())
}

//
// Setup lookup table
//
func rutMgrSetupLog2DistLKT(lkt []int) DhtErrno {
	var n uint
	var b uint
	lkt[0] = 8
	for n = 0; n < 8; n++ {
		for b = 1<<n; b < 1<<(n+1); b++ {
			lkt[b] = int(8 - n - 1)
		}
	}
	return DhtEnoNone
}

//
// Caculate the distance between two nodes.
// Notice: the return "d" more larger, it's more closer
//
func (rutMgr *RutMgr)rutMgrLog2Dist(h1 *Hash, h2 *Hash) int {
	var d = 0
	for i, b := range h2 {
		delta := rutMgr.distLookupTab[h1[i] ^ b]
		d += delta
		if delta != 8 {
			break
		}
	}
	return d
}

//
// Setup route table
//
func (rutMgr *RutMgr)rutMgrSetupRouteTable() DhtErrno {
	rt := &rutMgr.rutTab
	rt.shaLocal = *rutMgrNodeId2Hash(rutMgr.localNodeId)
	rt.bucketSize = rutMgrBucketSize
	rt.maxLatency = rutMgrMaxLatency
	rt.bucketTab = make([]*list.List, 0, HashBitLength + 1)
	rt.metricTab = make(map[config.NodeID]*rutMgrPeerMetric, 0)
	return DhtEnoNone
}

//
// Metric sample input
//
func (rt *rutMgrRouteTable) rutMgrMetricSample(id config.NodeID, latency time.Duration) DhtErrno {

	if m, dup := rt.metricTab[id]; dup {
		m.ltnSamples = append(m.ltnSamples, latency)
		return rt.rutMgrMetricUpdate(id)
	}

	rt.metricTab[id] = &rutMgrPeerMetric {
		peerId:		id,
		ltnSamples: []time.Duration{latency},
		ewma:		latency,
	}

	return DhtEnoNone
}

//
// Metric update EWMA about latency
//
func (rt *rutMgrRouteTable) rutMgrMetricUpdate(id config.NodeID) DhtErrno {
	return DhtEnoNone
}

//
// Metric get EWMA latency of peer
//
func (rt *rutMgrRouteTable) rutMgrMetricGetEWMA(id config.NodeID) (DhtErrno, time.Duration){
	mt := rt.metricTab
	if m, ok := mt[id]; ok {
		return DhtEnoNone, m.ewma
	}
	return DhtEnoNotFound, -1
}

//
// Node identity to hash(sha)
//
func rutMgrNodeId2Hash(id config.NodeID) *Hash {
	h := sha256.Sum256(id[:])
	return (*Hash)(&h)
}

//
// Sort peers with distance
//
func rutMgrSortPeer(ps []*rutMgrBucketNode, ds []int) {

	if len(ps) == 0 || len(ds) == 0 {
		return
	}

	li := list.New()

	for i, d := range ds {
		inserted := false
		for el := li.Front(); el != nil; el = el.Next() {
			if d < ds[el.Value.(int)] {
				li.InsertBefore(i, el)
				inserted = true
				break;
			}
		}
		if !inserted {
			li.PushBack(i)
		}
	}

	i := 0
	for el := li.Front(); el != nil; el = el.Next() {
		pi := el.Value.(int)
		ps[i], ps[pi] = ps[pi], ps[i]
		ds[i], ds[pi] = ds[pi], ds[i]
		i++
	}
}

//
// Lookup node
//
func (rutMgr *RutMgr)rutMgrFind(id config.NodeID) (DhtErrno, *list.Element) {
	hash := rutMgrNodeId2Hash(id)
	dist := rutMgr.rutMgrLog2Dist(&rutMgr.rutTab.shaLocal, hash)
	return rutMgr.rutTab.find(id, dist)
}

//
// Lookup node in buckets
//
func (rt *rutMgrRouteTable)find(id config.NodeID, dist int) (DhtErrno, *list.Element) {

	if dist >= len(rt.bucketTab) {
		return DhtEnoNotFound, nil
	}

	li := rt.bucketTab[dist]
	for el := li.Front(); el != nil; el.Next() {
		if el.Value.(*rutMgrBucketNode).node.ID == id {
			return DhtEnoNone, el
		}
	}

	return DhtEnoNotFound, nil
}

//
// Update route table
//
func (rt *rutMgrRouteTable)update(bn *rutMgrBucketNode, dist int) DhtErrno {

	tail := len(rt.bucketTab)
	if tail == 0 {
		rt.bucketTab[0] = list.New()
	} else {
		tail--
	}

	if eno, el := rt.find(bn.node.ID, dist); eno == DhtEnoNone && el != nil {
		rt.bucketTab[dist].MoveToFront(el)
		return DhtEnoNone
	}

	eno, ewma := rt.rutMgrMetricGetEWMA(bn.node.ID)
	if eno != DhtEnoNone {
		log.LogCallerFileLine("update: " +
			"rutMgrMetricGetEWMA failed, eno: %d, ewma: %d",
			eno, ewma)
		return eno
	}

	if ewma > rt.maxLatency {
		log.LogCallerFileLine("update: " +
			"discarded, ewma: %d,  maxLatency: %d",
			ewma, rt.maxLatency)
		return DhtEnoNone
	}

	tailBucket := rt.bucketTab[tail]
	if tailBucket.PushBack(bn); tailBucket.Len() > rt.bucketSize {
		rt.split(tailBucket, tail)
	}

	return DhtEnoNone
}

//
// Split the tail bucket
//
func (rt *rutMgrRouteTable)split(li *list.List, dist int) DhtErrno {

	if len(rt.bucketTab) - 1 != dist {
		log.LogCallerFileLine("split: can only split the tail bucket")
		return DhtEnoParameter
	}

	if li.Len() == 0 {
		log.LogCallerFileLine("split: can't split an empty bucket")
		return DhtEnoParameter
	}

	newLi := list.New()

	var el = li.Front()
	var elNext *list.Element = nil

	for {
		elNext = el.Next()
		bn := el.Value.(rutMgrBucketNode)
		if bn.dist > dist {
			newLi.PushBack(el)
			li.Remove(el)
		}
		if elNext == nil {
			break
		}
		el = elNext
	}

	for li.Len() > rt.bucketSize {
		li.Remove(li.Back())
	}

	if newLi.Len() != 0 {
		rt.bucketTab = append(rt.bucketTab, newLi)
	}

	if newLi.Len() > rt.bucketSize {
		rt.split(newLi, dist + 1)
	}

	return DhtEnoNone
}

//
// Register notifee
//
func (rutMgr *RutMgr)rutMgrNotifeeReg(
	task	interface{},
	id		*config.NodeID,
	max		int,
	bns		[]*rutMgrBucketNode,
	ds		[]int) DhtErrno {

	if len(rutMgr.ntfTab) >= rutMgrMaxNofifee {
		log.LogCallerFileLine("rutMgrNotifeeReg: too much notifees, max: %d", rutMgrMaxNofifee)
		return DhtEnoResource
	}

	nid := rutMgrNotifeeId {
		task:	task,
		target:	*id,
	}

	ntfe := rutMgrNotifee {
		id:			nid,
		max:		max,
		nearests:	bns,
		dists:		ds,
	}

	rutMgr.ntfTab[nid] = &ntfe

	return DhtEnoNone
}

//
// Notify those tasks whom registered with notifees
//
func (rutMgr *RutMgr)rutMgrNotify() DhtErrno {

	var ind = sch.MsgDhtRutMgrNotificationInd{}
	var msg = sch.SchMessage{}
	var failCnt = 0

	for key, ntf := range rutMgr.ntfTab {

		task := ntf.id.task
		target := &ntf.id.target
		size := ntf.max

		eno, nearest, dist := rutMgr.rutMgrNearest(target, size)
		if eno != DhtEnoNone {
			log.LogCallerFileLine("rutMgrNotify: rutMgrNearest failed, eno: %d", eno)
			failCnt++
			continue
		}

		rutMgr.ntfTab[key].nearests = nearest
		rutMgr.ntfTab[key].dists = dist

		ind.Target = *target
		ind.Peers = nearest
		ind.Dists = dist

		rutMgr.sdl.SchMakeMessage(&msg, rutMgr.ptnMe, task, sch.EvDhtRutMgrNotificationInd, &ind)
		rutMgr.sdl.SchSendMessage(&msg)
	}

	return DhtEnoNone
}

//
// Get nearest peers for target
//
func (rutMgr *RutMgr)rutMgrNearest(target *config.NodeID, size int) (DhtErrno, []*rutMgrBucketNode, []int){

	var nearest = make([]*rutMgrBucketNode, 0, rutMgrMaxNearest)
	var nearestDist = make([]int, 0, rutMgrMaxNearest)

	var count = 0
	var dhtEno DhtErrno = DhtEnoNone

	ht := rutMgrNodeId2Hash(*target)
	dt := rutMgr.rutMgrLog2Dist(&rutMgr.rutTab.shaLocal, ht)

	var addClosest = func (bk *list.List) int {
		count = len(nearest)
		if bk != nil {
			for el := bk.Front(); el != nil; el = el.Next() {
				peer := el.Value.(*rutMgrBucketNode)
				nearest = append(nearest, peer)
				dt := rutMgr.rutMgrLog2Dist(ht, &peer.hash)
				nearestDist = append(nearestDist, dt)
				if count++; count >= size {
					break
				}
			}
		}

		return count
	}

	if size <= 0 || size > rutMgrMaxNearest {
		log.LogCallerFileLine("rutMgrNearest: " +
			"invalid size: %d, min: 1, max: %d",
			size, rutMgrMaxNearest)

		dhtEno = DhtEnoParameter
		goto _done
	}

	//
	// the most closest bank
	//

	if bk := rutMgr.rutTab.bucketTab[dt]; bk != nil {
		if addClosest(bk) >= size {
			goto _done
		}
	}

	//
	// the second closest bank
	//

	for loop := dt + 1; loop < len(rutMgr.rutTab.bucketTab); loop++ {
		if bk := rutMgr.rutTab.bucketTab[loop]; bk != nil {
			if addClosest(bk) >= size {
				goto _done
			}
		}
	}

	if dt <= 0 { goto _done }

	//
	// the last bank
	//

	for loop := dt - 1; loop >= 0; loop-- {
		if bk := rutMgr.rutTab.bucketTab[loop]; bk != nil {
			if addClosest(bk) >= size {
				goto _done
			}
		}
	}

	//
	// response to the sender
	//

_done:

	if dhtEno != DhtEnoNone  {
		return dhtEno, nil, nil
	}

	if len(nearest) > 0 {
		rutMgrSortPeer(nearest, nearestDist)
	}

	return DhtEnoNone, nearest, nearestDist
}