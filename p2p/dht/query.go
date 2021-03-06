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
	"bytes"
	"container/list"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	config "github.com/yeeco/gyee/p2p/config"
	p2plog "github.com/yeeco/gyee/p2p/logger"
	nat "github.com/yeeco/gyee/p2p/nat"
	sch "github.com/yeeco/gyee/p2p/scheduler"
)

//
// debug
//
type qryMgrLogger struct {
	debug__ bool
}

var qryLog = qryMgrLogger{
	debug__: false,
}

func (log qryMgrLogger) Debug(fmt string, args ...interface{}) {
	if log.debug__ {
		p2plog.Debug(fmt, args...)
	}
}

//
// Constants
//
const (
	QryMgrName        = sch.DhtQryMgrName                         // query manage name registered in shceduler
	QryMgrMailboxSize = 1024 * 8								  // mail box size
	qryMgrMaxPendings = 64                                        // max pendings can be held in the list
	qryMgrMaxActInsts = 8                                         // max concurrent actived instances for one query
	qryMgrQryExpired  = time.Second * 60                          // duration to get expired for a query
	qryMgrQryMaxWidth = 64                                        // not the true "width", the max number of peers queryied
	qryMgrQryMaxDepth = 8                                         // the max depth for a query
	qryInstExpired    = time.Second * 16                          // duration to get expired for a query instance
	natMapKeepTime    = nat.MinKeepDuration                       // NAT map keep time
	natMapRefreshTime = nat.MinKeepDuration - nat.MinRefreshDelta // NAT map refresh time
)

//
// Query manager configuration
//
type qryMgrCfg struct {
	local          *config.Node  // pointer to local node specification
	maxPendings    int           // max pendings can be held in the list
	maxActInsts    int           // max concurrent actived instances for one query
	qryExpired     time.Duration // duration to get expired for a query
	qryInstExpired time.Duration // duration to get expired for a query instance
}

//
// Query control block status
//
type QryStatus = int

const (
	qsNull      = iota // null state
	qsPreparing        // in preparing, waiting for the nearest response from route manager
	qsInited           // had been initialized
)

//
// Query result node info
//
type qryResultInfo struct {
	node config.Node        // peer node info
	pcs  conMgrPeerConnStat // connection status
	dist int                // distance from local node
}

//
// Query pending node info
//
type qryPendingInfo struct {
	rutMgrBucketNode     // bucket node
	depth            int // depth
}

//
// Query control block
//
type qryCtrlBlock struct {
	ptnOwner   interface{}                         // owner task node pointer
	qryReq     *sch.MsgDhtQryMgrQueryStartReq      // original query request message
	seq        int                                 // sequence number
	forWhat    int                                 // what's the query control block for
	target     config.DsKey                        // target for looking up
	status     QryStatus                           // query status
	qryHistory map[config.NodeID]*qryPendingInfo   // history peers had been queried
	qryPending *list.List                          // pending peers to be queried, with type qryPendingInfo
	qryActived map[config.NodeID]*qryInstCtrlBlock // queries activated
	qryResult  *list.List                          // list of qryResultNodeInfo type object
	qryTid     int                                 // query timer identity
	icbSeq     int                                 // query instance control block sequence number
	rutNtfFlag bool                                // if notification asked for
	width      int                                 // the current number of peer had been queried
	depth      int                                 // the current max depth of query
}

//
// Query instance status
//
const (
	qisNull         = iota // null state
	qisInited              // had been inited to ready to start
	qisWaitConnect         // connect request sent, wait response from connection manager task
	qisWaitResponse        // query sent, wait response from peer
	qisDone                // done with exception
	qisDoneOk              // done normally
)

//
// Query instance control block
//
type qryInstCtrlBlock struct {
	sdl        *sch.Scheduler                 // pointer to scheduler
	sdlName    string						  // scheduler name
	seq        int                            // sequence number
	qryReq     *sch.MsgDhtQryMgrQueryStartReq // original query request message
	name       string                         // instance name
	ptnInst    interface{}                    // pointer to query instance task node
	ptnConMgr  interface{}                    // pointer to connection manager task node
	ptnQryMgr  interface{}                    // pointer to query manager task node
	ptnRutMgr  interface{}                    // pointer to rute manager task node
	status     int                            // instance status
	local      *config.Node                   // pointer to local node specification
	target     config.DsKey                   // target is looking up
	to         config.Node                    // to whom the query message sent
	dir        int                            // connection direction
	qTid       int                            // query timer identity
	begTime    time.Time                      // query begin time
	endTime    time.Time                      // query end time
	conBegTime time.Time                      // time to start connection
	conEndTime time.Time                      // time connection established
	depth      int                            // the current depth of the query instance
}

//
// Query manager
//
type QryMgr struct {
	sdl          *sch.Scheduler                 // pointer to scheduler
	name         string                         // query manager name
	tep          sch.SchUserTaskEp              // task entry
	ptnMe        interface{}                    // pointer to task node of myself
	ptnRutMgr    interface{}                    // pointer to task node of route manager
	ptnDhtMgr    interface{}                    // pointer to task node of dht manager
	ptnNatMgr    interface{}                    // pointer to task naode of nat manager
	instSeq      int                            // query instance sequence number
	qcbTab       map[config.DsKey]*qryCtrlBlock // query control blocks
	qmCfg        qryMgrCfg                      // query manager configuration
	qcbSeq       int                            // query control block sequence number
	natTcpResult bool                           // result about nap mapping for tcp
	pubTcpIp     net.IP                         // should be same as pubUdpIp
	pubTcpPort   int                            // public port form nat to be announced for tcp
}

//
// Create query manager
//
func NewQryMgr() *QryMgr {

	qmCfg := qryMgrCfg{
		maxPendings:    qryMgrMaxPendings,
		maxActInsts:    qryMgrMaxActInsts,
		qryExpired:     qryMgrQryExpired,
		qryInstExpired: qryInstExpired,
	}

	qryMgr := QryMgr{
		name:       QryMgrName,
		instSeq:    0,
		qcbTab:     map[config.DsKey]*qryCtrlBlock{},
		qmCfg:      qmCfg,
		qcbSeq:     0,
		pubTcpIp:   net.IPv4zero,
		pubTcpPort: 0,
	}

	qryMgr.tep = qryMgr.qryMgrProc

	return &qryMgr
}

//
// Entry point exported to shceduler
//
func (qryMgr *QryMgr) TaskProc4Scheduler(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {
	return qryMgr.tep(ptn, msg)
}

//
// Query manager entry
//
func (qryMgr *QryMgr) qryMgrProc(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {
	qryLog.Debug("qryMgrProc: ptn: %p, msg.Id: %d", ptn, msg.Id)
	eno := sch.SchEnoUnknown
	switch msg.Id {

	case sch.EvSchPoweron:
		eno = qryMgr.poweron(ptn)

	case sch.EvSchPoweroff:
		eno = qryMgr.poweroff(ptn)

	case sch.EvDhtQryMgrQueryStartReq:
		sender := qryMgr.sdl.SchGetSender(msg)
		eno = qryMgr.queryStartReq(sender, msg.Body.(*sch.MsgDhtQryMgrQueryStartReq))

	case sch.EvDhtRutMgrNearestRsp:
		eno = qryMgr.rutNearestRsp(msg.Body.(*sch.MsgDhtRutMgrNearestRsp))

	case sch.EvDhtQryMgrQcbTimer:
		qcb := msg.Body.(*qryCtrlBlock)
		eno = qryMgr.qcbTimerHandler(qcb)

	case sch.EvDhtQryMgrQueryStopReq:
		sender := qryMgr.sdl.SchGetSender(msg)
		eno = qryMgr.queryStopReq(sender, msg.Body.(*sch.MsgDhtQryMgrQueryStopReq))

	case sch.EvDhtRutMgrNotificationInd:
		eno = qryMgr.rutNotificationInd(msg.Body.(*sch.MsgDhtRutMgrNotificationInd))

	case sch.EvDhtQryInstStatusInd:
		eno = qryMgr.instStatusInd(msg.Body.(*sch.MsgDhtQryInstStatusInd))

	case sch.EvDhtQryInstResultInd:
		eno = qryMgr.instResultInd(msg.Body.(*sch.MsgDhtQryInstResultInd))

	case sch.EvNatMgrReadyInd:
		eno = qryMgr.natMgrReadyInd(msg.Body.(*sch.MsgNatMgrReadyInd))

	case sch.EvNatMgrMakeMapRsp:
		eno = qryMgr.natMakeMapRsp(msg)

	case sch.EvNatMgrPubAddrUpdateInd:
		eno = qryMgr.natPubAddrUpdateInd(msg)

	default:
		qryLog.Debug("qryMgrProc: unknown event: %d", msg.Id)
		eno = sch.SchEnoParameter
	}

	qryLog.Debug("qryMgrProc: get out, ptn: %p, msg.Id: %d", ptn, msg.Id)

	return eno
}

//
// Poweron handler
//
func (qryMgr *QryMgr) poweron(ptn interface{}) sch.SchErrno {
	var eno sch.SchErrno
	qryMgr.ptnMe = ptn
	if qryMgr.sdl = sch.SchGetScheduler(ptn); qryMgr.sdl == nil {
		qryLog.Debug("poweron: nil scheduler")
		return sch.SchEnoInternal
	}
	if eno, qryMgr.ptnDhtMgr = qryMgr.sdl.SchGetUserTaskNode(DhtMgrName); eno != sch.SchEnoNone {
		qryLog.Debug("poweron: get task failed, task: %s", DhtMgrName)
		return eno
	}
	if eno, qryMgr.ptnRutMgr = qryMgr.sdl.SchGetUserTaskNode(RutMgrName); eno != sch.SchEnoNone {
		qryLog.Debug("poweron: get task failed, task: %s", RutMgrName)
		return eno
	}
	if eno, qryMgr.ptnNatMgr = qryMgr.sdl.SchGetUserTaskNode(nat.NatMgrName); eno != sch.SchEnoNone {
		qryLog.Debug("poweron: get task failed, task: %s", nat.NatMgrName)
		return eno
	}
	if dhtEno := qryMgr.qryMgrGetConfig(); dhtEno != DhtEnoNone {
		qryLog.Debug("poweron: qryMgrGetConfig failed, dhtEno: %d", dhtEno)
		return sch.SchEnoUserTask
	}
	mapQrySeqLock[qryMgr.sdl.SchGetP2pCfgName()] = sync.Mutex{}
	return sch.SchEnoNone
}

//
// Poweroff handler
//
func (qryMgr *QryMgr) poweroff(ptn interface{}) sch.SchErrno {
	qryLog.Debug("poweroff: task will be done ...")
	for _, qcb := range qryMgr.qcbTab {
		for _, qry := range qcb.qryActived {
			po := sch.SchMessage{}
			qryMgr.sdl.SchMakeMessage(&po, qryMgr.ptnMe, qry.ptnInst, sch.EvSchPoweroff, nil)
			po.TgtName = qry.name
			qryMgr.sdl.SchSendMessage(&po)
		}
	}
	return qryMgr.sdl.SchTaskDone(qryMgr.ptnMe, qryMgr.name, sch.SchEnoKilled)
}

//
// Query start request handler
//
func (qryMgr *QryMgr) queryStartReq(sender interface{}, msg *sch.MsgDhtQryMgrQueryStartReq) sch.SchErrno {
	if sender == nil || msg == nil {
		qryLog.Debug("queryStartReq: invalid prameters")
		return sch.SchEnoParameter
	}
	if msg.ForWhat != MID_PUTVALUE &&
		msg.ForWhat != MID_PUTPROVIDER &&
		msg.ForWhat != MID_FINDNODE &&
		msg.ForWhat != MID_GETPROVIDER_REQ &&
		msg.ForWhat != MID_GETVALUE_REQ {
		qryLog.Debug("queryStartReq: unknown what's for")
		return sch.SchEnoMismatched
	}

	qryLog.Debug("queryStartReq: ForWhat: %d, sender: %s",
		msg.ForWhat, qryMgr.sdl.SchGetTaskName(sender))

	var forWhat = msg.ForWhat
	var rsp = sch.MsgDhtQryMgrQueryStartRsp{Target: msg.Target, Eno: DhtEnoUnknown.GetEno()}
	var qcb *qryCtrlBlock
	var schMsg *sch.SchMessage

	//
	// set "NtfReq" to be true to tell route manager that we need notifications,
	// see handler about this event in route.go pls.
	//

	var nearestReq = sch.MsgDhtRutMgrNearestReq{
		Target:  msg.Target,
		Max:     rutMgrMaxNearest,
		NtfReq:  true,
		Task:    qryMgr.ptnMe,
		ForWhat: forWhat,
	}

	if _, dup := qryMgr.qcbTab[msg.Target]; dup {
		qryLog.Debug("queryStartReq: duplicated target: %x", msg.Target)
		rsp.Eno = DhtEnoDuplicated.GetEno()
		goto _rsp2Sender
	}

	qcb = new(qryCtrlBlock)
	qcb.ptnOwner = sender
	qcb.qryReq = msg
	qcb.seq = qryMgr.qcbSeq
	qryMgr.qcbSeq++
	qcb.forWhat = forWhat
	qcb.icbSeq = 0
	qcb.target = msg.Target
	qcb.status = qsNull
	qcb.qryHistory = make(map[config.NodeID]*qryPendingInfo, 0)
	qcb.qryPending = nil
	qcb.qryActived = make(map[config.NodeID]*qryInstCtrlBlock, qryMgr.qmCfg.maxActInsts)
	qcb.qryResult = nil
	qcb.rutNtfFlag = nearestReq.NtfReq
	qcb.status = qsPreparing
	qcb.width = 0
	qcb.depth = 0
	qryMgr.qcbTab[msg.Target] = qcb

	qryLog.Debug("queryStartReq: qcb: %+v", *qcb)

	schMsg = new(sch.SchMessage)
	qryMgr.sdl.SchMakeMessage(schMsg, qryMgr.ptnMe, qryMgr.ptnRutMgr, sch.EvDhtRutMgrNearestReq, &nearestReq)
	qryMgr.sdl.SchSendMessage(schMsg)
	rsp.Eno = DhtEnoNone.GetEno()

_rsp2Sender:

	schMsg = new(sch.SchMessage)
	qryMgr.sdl.SchMakeMessage(schMsg, qryMgr.ptnMe, sender, sch.EvDhtQryMgrQueryStartRsp, &rsp)
	qryMgr.sdl.SchSendMessage(schMsg)
	if rsp.Eno != DhtEnoNone.GetEno() {
		return sch.SchEnoUserTask
	}
	return sch.SchEnoNone
}

//
// Nearest response handler
//
func (qryMgr *QryMgr) rutNearestRsp(msg *sch.MsgDhtRutMgrNearestRsp) sch.SchErrno {
	if msg == nil {
		qryLog.Debug("rutNearestRsp: invalid parameter")
		return sch.SchEnoParameter
	}

	qryLog.Debug("rutNearestRsp: msg: %+v", msg)

	if msg.ForWhat != MID_PUTVALUE &&
		msg.ForWhat != MID_PUTPROVIDER &&
		msg.ForWhat != MID_FINDNODE &&
		msg.ForWhat != MID_GETPROVIDER_REQ &&
		msg.ForWhat != MID_GETVALUE_REQ {
		qryLog.Debug("rutNearestRsp: unknown what's for")
		return sch.SchEnoMismatched
	}

	qryLog.Debug("rutNearestRsp: ForWhat: %d", msg.ForWhat)

	forWhat := msg.ForWhat
	target := msg.Target
	qcb, ok := qryMgr.qcbTab[target]
	if !ok {
		qryLog.Debug("rutNearestRsp: qcb not exist, target: %x", target)
		return sch.SchEnoNotFound
	}
	if qcb == nil {
		qryLog.Debug("rutNearestRsp: nil qcb, target: %x", target)
		return sch.SchEnoInternal
	}
	if qcb.status != qsPreparing {
		qryLog.Debug("rutNearestRsp: qcb status mismatched, status: %d, target: %x",
			qcb.status, target)
		return sch.SchEnoMismatched
	}

	qryFailed2Sender := func(eno DhtErrno) {
		rsp := sch.MsgDhtQryMgrQueryStartRsp{
			Target: msg.Target,
			Eno:    int(eno),
		}
		schMsg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&schMsg, qryMgr.ptnMe, qcb.ptnOwner, sch.EvDhtQryMgrQueryStartRsp, &rsp)
		qryMgr.sdl.SchSendMessage(&schMsg)
	}

	qryOk2Sender := func(peer *config.Node) {
		ind := sch.MsgDhtQryMgrQueryResultInd{
			Eno:     DhtEnoNone.GetEno(),
			ForWhat: msg.ForWhat,
			Target:  target,
			Peers:   []*config.Node{peer},
		}
		schMsg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&schMsg, qryMgr.ptnMe, qcb.ptnOwner, sch.EvDhtQryMgrQueryResultInd, &ind)
		qryMgr.sdl.SchSendMessage(&schMsg)
	}

	if msg.Eno != DhtEnoNone.GetEno() {
		qryFailed2Sender(DhtErrno(msg.Eno))
		qryMgr.qryMgrDelQcb(delQcb4NoSeeds, target)
		return sch.SchEnoNone
	}

	if (msg.Eno == DhtEnoNone.GetEno()) && (msg.Peers == nil || msg.Pcs == nil) {
		qryLog.Debug("rutNearestRsp: invalid empty nearest set reported")
		qryFailed2Sender(DhtEnoRoute)
		qryMgr.qryMgrDelQcb(delQcb4NoSeeds, target)
		return sch.SchEnoParameter
	}

	peers := msg.Peers.([]*rutMgrBucketNode)
	pcs := msg.Pcs.([]int)
	dists := msg.Dists.([]int)
	if (msg.Eno == DhtEnoNone.GetEno()) && (len(peers) != len(pcs) || len(peers) != len(dists)) {
		qryLog.Debug("rutNearestRsp: invalid nearest set reported")
		qryFailed2Sender(DhtEnoRoute)
		qryMgr.qryMgrDelQcb(delQcb4NoSeeds, target)
		return sch.SchEnoParameter
	}

	if len(peers) == 0 {
		qryLog.Debug("rutNearestRsp: invalid empty nearest set reported")
		qryFailed2Sender(DhtEnoRoute)
		qryMgr.qryMgrDelQcb(delQcb4NoSeeds, target)
		return sch.SchEnoParameter
	}

	//
	// check if target found in local while updating the query result by the
	// nearests reported.
	//

	qcb.qryResult = list.New()
	for idx, peer := range peers {
		if forWhat == MID_FINDNODE ||
			forWhat == MID_GETPROVIDER_REQ ||
			forWhat == MID_GETVALUE_REQ {
			if bytes.Compare(peer.hash[0:], target[0:]) == 0 {
				qryLog.Debug("rutNearestRsp: target found: %x", target)
				qryOk2Sender(&peer.node)
				qryMgr.qryMgrDelQcb(delQcb4TargetInLocal, target)
				return sch.SchEnoNone
			}
		}

		qri := qryResultInfo{
			node: peer.node,
			pcs:  pcs[idx],
			dist: dists[idx],
		}
		qcb.qcbUpdateResult(&qri)
	}

	//
	// start queries by putting nearests to pending queue and then putting
	// pending nodes to be activated.
	//

	qcb.qryPending = list.New()
	pendInfo := make([]*qryPendingInfo, 0)
	for idx := 0; idx < len(peers); idx++ {
		pi := qryPendingInfo{
			rutMgrBucketNode: *peers[idx],
			depth:            0,
		}
		pendInfo = append(pendInfo, &pi)
	}

	var dhtEno = DhtErrno(DhtEnoNone)
	if dhtEno = qcb.qryMgrQcbPutPending(pendInfo, qryMgr.qmCfg.maxPendings); dhtEno == DhtEnoNone {
		if dhtEno = qryMgr.qryMgrQcbStartTimer(qcb); dhtEno == DhtEnoNone {
			qryMgr.qryMgrQcbPutActived(qcb)
			qcb.status = qsInited
			return sch.SchEnoNone
		}
	}

	qryLog.Debug("rutNearestRsp: qryMgrQcbPutPending failed, eno: %d", dhtEno)
	qryFailed2Sender(dhtEno)
	qryMgr.qryMgrDelQcb(delQcb4NoSeeds, target)
	return sch.SchEnoResource

}

//
// Query stop request handler
//
func (qryMgr *QryMgr) queryStopReq(sender interface{}, msg *sch.MsgDhtQryMgrQueryStopReq) sch.SchErrno {
	target := msg.Target
	rsp := sch.MsgDhtQryMgrQueryStopRsp{Target: target, Eno: DhtEnoNone.GetEno()}
	rsp2Sender := func(rsp *sch.MsgDhtQryMgrQueryStopRsp) sch.SchErrno {
		schMsg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&schMsg, qryMgr.ptnMe, sender, sch.EvDhtQryMgrQueryStopRsp, rsp)
		return qryMgr.sdl.SchSendMessage(&schMsg)
	}
	rsp.Eno = int(qryMgr.qryMgrDelQcb(delQcb4Command, target))
	return rsp2Sender(&rsp)
}

//
//Route notification handler
//
func (qryMgr *QryMgr) rutNotificationInd(msg *sch.MsgDhtRutMgrNotificationInd) sch.SchErrno {
	qcb := (*qryCtrlBlock)(nil)
	target := msg.Target
	if qcb = qryMgr.qcbTab[target]; qcb == nil {
		qryLog.Debug("rutNotificationInd: target not found: %x", target)
		return sch.SchEnoParameter
	}

	if qcb.status != qsInited {
		qryLog.Debug("rutNotificationInd: query not inited yet for target: %x", target)
		return sch.SchEnoUserTask
	}

	qpi := msg.Peers.([]*rutMgrBucketNode)
	pendInfo := make([]*qryPendingInfo, 0)
	for idx := 0; idx < len(qpi); idx++ {
		var pi = qryPendingInfo{
			rutMgrBucketNode: *qpi[idx],
			depth:            0,
		}
		pendInfo = append(pendInfo, &pi)
	}

	qcb.qryMgrQcbPutPending(pendInfo, qryMgr.qmCfg.maxPendings)
	qryMgr.qryMgrQcbPutActived(qcb)

	if qcb.qryPending.Len() > 0 && len(qcb.qryActived) < qryMgr.qmCfg.maxActInsts {
		qryLog.Debug("rutNotificationInd: internal errors")
		return sch.SchEnoUserTask
	}

	if qcb.qryPending.Len() == 0 && len(qcb.qryActived) == 0 {
		if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNotFound.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
			qryLog.Debug("rutNotificationInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
			return sch.SchEnoUserTask
		}
		if dhtEno := qryMgr.qryMgrDelQcb(delQcb4NoMoreQueries, qcb.target); dhtEno != DhtEnoNone {
			qryLog.Debug("rutNotificationInd: qryMgrDelQcb failed, dhtEno: %d", dhtEno)
			return sch.SchEnoUserTask
		}
	}

	return sch.SchEnoNone
}

//
// Instance status indication handler
//
func (qryMgr *QryMgr) instStatusInd(msg *sch.MsgDhtQryInstStatusInd) sch.SchErrno {
	switch msg.Status {
	case qisNull:
		qryLog.Debug("instStatusInd: qisNull")
	case qisInited:
		qryLog.Debug("instStatusInd: qisInited")
	case qisWaitConnect:
		qryLog.Debug("instStatusInd: qisWaitConnect")
	case qisWaitResponse:
		qryLog.Debug("instStatusInd: qisWaitResponse")
	case qisDoneOk:
		qryLog.Debug("instStatusInd: qisDoneOk")

	case qisDone:
		qryLog.Debug("instStatusInd: qisDone")
		qcb, exist := qryMgr.qcbTab[msg.Target]
		if !exist {
			qryLog.Debug("instStatusInd: qcb not found")
			return sch.SchEnoNotFound
		}
		if dhtEno := qryMgr.qryMgrDelIcb(delQcb4QryInstDoneInd, &msg.Target, &msg.Peer); dhtEno != DhtEnoNone {
			qryLog.Debug("instStatusInd: qryMgrDelIcb failed, eno: %d", dhtEno)
			return sch.SchEnoUserTask
		}
		if eno, num := qryMgr.qryMgrQcbPutActived(qcb); true {
			qryLog.Debug("instStatusInd: qryMgrQcbPutActived return with eno: %d, num: %d", eno, num)
		}
		if qcb.qryPending.Len() > 0 && len(qcb.qryActived) < qryMgr.qmCfg.maxActInsts {
			qryLog.Debug("instStatusInd: internal errors")
			return sch.SchEnoUserTask
		}

		// if pending queue and active queue all are empty, we just report query
		// result and end the query.
		qryLog.Debug("instStatusInd: pending: %d, actived: %d", qcb.qryPending.Len(), len(qcb.qryActived))

		if qcb.qryPending.Len() == 0 && len(qcb.qryActived) == 0 {
			qryLog.Debug("instStatusInd: query done: %x", qcb.target)
			if qcb.forWhat == MID_PUTVALUE || qcb.forWhat == MID_PUTPROVIDER {
				if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNone.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
					qryLog.Debug("instStatusInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
					return sch.SchEnoUserTask
				}
			} else {
				if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNotFound.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
					qryLog.Debug("instStatusInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
					return sch.SchEnoUserTask
				}
			}
			if dhtEno := qryMgr.qryMgrDelQcb(delQcb4NoMoreQueries, qcb.target); dhtEno != DhtEnoNone {
				qryLog.Debug("instStatusInd: qryMgrDelQcb failed, dhtEno: %d", dhtEno)
				return sch.SchEnoUserTask
			}
		}

	default:
		qryLog.Debug("instStatusInd: invalid instance status: %d", msg.Status)
		return sch.SchEnoUserTask
	}

	return sch.SchEnoNone
}

//
// Instance query result indication handler
//
func (qryMgr *QryMgr) instResultInd(msg *sch.MsgDhtQryInstResultInd) sch.SchErrno {
	if msg == nil {
		qryLog.Debug("instResultInd: invalid parameter")
		return sch.SchEnoParameter
	}

	qryLog.Debug("instResultInd: msg: %+v", *msg)
	if msg.ForWhat != sch.EvDhtMgrPutValueReq &&
		msg.ForWhat != sch.EvDhtMgrPutProviderReq &&
		msg.ForWhat != sch.EvDhtConInstNeighbors &&
		msg.ForWhat != sch.EvDhtConInstGetProviderRsp &&
		msg.ForWhat != sch.EvDhtConInstGetValRsp {
		qryLog.Debug("instResultInd: mismatched, it's %d", msg.ForWhat)
		return sch.SchEnoMismatched
	}

	qryLog.Debug("instResultInd: ForWhat: %d", msg.ForWhat)
	var (
		qcb      *qryCtrlBlock = nil
		qpiList                = make([]*qryPendingInfo, 0)
		hashList               = make([]*Hash, 0)
		distList               = make([]int, 0)
		rutMgr                 = qryMgr.sdl.SchGetTaskObject(RutMgrName).(*RutMgr)
	)

	if len(msg.Peers) != len(msg.Pcs) {
		qryLog.Debug("instResultInd: mismatched Peers and Pcs")
		return sch.SchEnoMismatched
	}
	if rutMgr == nil {
		qryLog.Debug("instResultInd: nil route manager")
		return sch.SchEnoInternal
	}

	for idx, peer := range msg.Peers {
		if bytes.Compare(peer.ID[0:], qryMgr.qmCfg.local.ID[0:]) == 0 {
			if idx != len(msg.Peers)-1 {
				msg.Peers = append(msg.Peers[0:idx], msg.Peers[idx+1:]...)
				msg.Pcs = append(msg.Pcs[0:idx], msg.Pcs[idx+1:]...)
			} else {
				msg.Peers = msg.Peers[0:idx]
				msg.Pcs = msg.Pcs[0:idx]
			}
			break
		}
	}

	from := msg.From
	latency := msg.Latency
	updateReq2RutMgr := func(peer *config.Node, dur time.Duration) sch.SchErrno {
		updateReq := sch.MsgDhtRutMgrUpdateReq{
			Why: rutMgrUpdate4Query,
			Eno: DhtEnoNone.GetEno(),
			Seens: []config.Node{
				*peer,
			},
			Duras: []time.Duration{
				dur,
			},
		}
		schMsg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&schMsg, qryMgr.ptnMe, qryMgr.ptnRutMgr, sch.EvDhtRutMgrUpdateReq, &updateReq)
		return qryMgr.sdl.SchSendMessage(&schMsg)
	}

	updateReq2RutMgr(&from, latency)

	target := msg.Target
	if qcb = qryMgr.qcbTab[target]; qcb == nil {
		qryLog.Debug("instResultInd: not found, target: %x", target)
		return sch.SchEnoUserTask
	}

	icb, ok := qcb.qryActived[from.ID]
	if !ok || icb == nil {
		qryLog.Debug("instResultInd: target not found: %x", target)
		return sch.SchEnoUserTask
	}

	depth := icb.depth

	for idx, peer := range msg.Peers {

		hash := rutMgrNodeId2Hash(peer.ID)
		hashList = append(hashList, hash)
		dist := rutMgr.rutMgrLog2Dist(nil, hash)
		distList = append(distList, dist)

		qri := qryResultInfo{
			node: *peer,
			pcs:  conMgrPeerConnStat(msg.Pcs[idx]),
			dist: distList[idx],
		}

		qcb.qcbUpdateResult(&qri)
	}

	if msg.ForWhat == sch.EvDhtConInstNeighbors {
		for _, peer := range msg.Peers {
			key := rutMgrNodeId2Hash(peer.ID)
			if bytes.Compare((*key)[0:], target[0:]) == 0 {
				qryMgr.qryMgrResultReport(qcb, DhtEnoNone.GetEno(), nil, msg.Value, msg.Provider)
				if dhtEno := qryMgr.qryMgrDelQcb(delQcb4TargetFound, qcb.target); dhtEno != DhtEnoNone {
					qryLog.Debug("instResultInd: qryMgrDelQcb failed, eno: %d", dhtEno)
					return sch.SchEnoUserTask
				}
				return sch.SchEnoNone
			}
		}

	} else if msg.ForWhat == sch.EvDhtConInstGetValRsp {
		if msg.Value != nil && len(msg.Value) > 0 {
			qryMgr.qryMgrResultReport(qcb, DhtEnoNone.GetEno(), nil, msg.Value, nil)
			if dhtEno := qryMgr.qryMgrDelQcb(delQcb4TargetFound, qcb.target); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrDelQcb failed, eno: %d", dhtEno)
				return sch.SchEnoUserTask
			}
			return sch.SchEnoNone
		}
	} else if msg.ForWhat == sch.EvDhtConInstGetProviderRsp {
		if msg.Provider != nil {
			qryMgr.qryMgrResultReport(qcb, DhtEnoNone.GetEno(), nil, nil, msg.Provider)
			if dhtEno := qryMgr.qryMgrDelQcb(delQcb4TargetFound, qcb.target); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrDelQcb failed, eno: %d", dhtEno)
				return sch.SchEnoUserTask
			}
			return sch.SchEnoNone
		}
	}

	if eno := qryMgr.qryMgrDelIcb(delQcb4QryInstResultInd, &msg.Target, &msg.From.ID); eno != DhtEnoNone {
		qryLog.Debug("instResultInd: qryMgrDelIcb failed, eno: %d", eno)
		return sch.SchEnoUserTask
	}

	if depth > qcb.depth {
		qcb.depth = depth
	}

	if msg.ForWhat == sch.EvDhtConInstNeighbors ||
		msg.ForWhat == sch.EvDhtConInstGetProviderRsp ||
		msg.ForWhat == sch.EvDhtConInstGetValRsp {
		if qcb.depth > qryMgrQryMaxDepth || len(qcb.qryHistory) >= qryMgrQryMaxWidth {
			qryLog.Debug("instResultInd: limited to stop query, depth: %d, width: %d", qcb.depth, len(qcb.qryHistory))
			if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNotFound.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
				return sch.SchEnoUserTask
			}
			if dhtEno := qryMgr.qryMgrDelQcb(delQcb4NoMoreQueries, qcb.target); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrDelQcb failed, dhtEno: %d", dhtEno)
				return sch.SchEnoUserTask
			}
			return sch.SchEnoNone
		}

		for idx, peer := range msg.Peers {
			qpi := qryPendingInfo{
				rutMgrBucketNode: rutMgrBucketNode{
					node: *peer,
					hash: *hashList[idx],
					dist: distList[idx],
				},
				depth: depth + 1,
			}
			qpiList = append(qpiList, &qpi)
		}

		qcb.qryMgrQcbPutPending(qpiList, qryMgr.qmCfg.maxPendings)
		qryMgr.qryMgrQcbPutActived(qcb)
	}

	if qcb.qryPending.Len() > 0 && len(qcb.qryActived) < qryMgr.qmCfg.maxActInsts {
		qryLog.Debug("instResultInd: internal errors")
		return sch.SchEnoUserTask
	}

	if qcb.qryPending.Len() == 0 && len(qcb.qryActived) == 0 {
		if msg.ForWhat == sch.EvDhtConInstNeighbors ||
			msg.ForWhat == sch.EvDhtConInstGetProviderRsp ||
			msg.ForWhat == sch.EvDhtConInstGetValRsp {
			if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNotFound.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
			}
		} else {
			if dhtEno := qryMgr.qryMgrResultReport(qcb, DhtEnoNone.GetEno(), nil, nil, nil); dhtEno != DhtEnoNone {
				qryLog.Debug("instResultInd: qryMgrResultReport failed, dhtEno: %d", dhtEno)
			}
		}

		if dhtEno := qryMgr.qryMgrDelQcb(delQcb4NoMoreQueries, qcb.target); dhtEno != DhtEnoNone {
			qryLog.Debug("instResultInd: qryMgrDelQcb failed, dhtEno: %d", dhtEno)
			return sch.SchEnoUserTask
		}
	}

	return sch.SchEnoNone
}

//
// nat ready to work
//
func (qryMgr *QryMgr) natMgrReadyInd(msg *sch.MsgNatMgrReadyInd) sch.SchErrno {
	qryLog.Debug("natMgrReadyInd: nat type: %s", msg.NatType)
	if msg.NatType == config.NATT_NONE {
		qryMgr.pubTcpIp = qryMgr.qmCfg.local.IP
		qryMgr.pubTcpPort = int(qryMgr.qmCfg.local.TCP)
	} else {
		req := sch.MsgNatMgrMakeMapReq{
			Proto:      "tcp",
			FromPort:   int(qryMgr.qmCfg.local.TCP),
			ToPort:     int(qryMgr.qmCfg.local.TCP),
			DurKeep:    natMapKeepTime,
			DurRefresh: natMapRefreshTime,
		}
		schMsg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&schMsg, qryMgr.ptnMe, qryMgr.ptnNatMgr, sch.EvNatMgrMakeMapReq, &req)
		if eno := qryMgr.sdl.SchSendMessage(&schMsg); eno != sch.SchEnoNone {
			qryLog.Debug("natMgrReadyInd: SchSendMessage failed, eno: %d", eno)
			return sch.SchEnoUserTask
		}
	}
	return sch.SchEnoNone
}

//
// nat make mapping response
//
func (qryMgr *QryMgr) natMakeMapRsp(msg *sch.SchMessage) sch.SchErrno {
	// see comments in function tabMgrNatMakeMapRsp for more please.
	mmr := msg.Body.(*sch.MsgNatMgrMakeMapRsp)
	if !nat.NatIsResultOk(mmr.Result) {
		qryLog.Debug("natMakeMapRsp: fail reported, mmr: %+v", *mmr)
	}
	qryLog.Debug("natMakeMapRsp: proto: %s, ip:port = %s:%d",
		mmr.Proto, mmr.PubIp.String(), mmr.PubPort)

	proto := strings.ToLower(mmr.Proto)
	if proto == "tcp" {
		qryMgr.natTcpResult = nat.NatIsStatusOk(mmr.Status)
		if qryMgr.natTcpResult {
			qryLog.Debug("natMakeMapRsp: public dht addr: %s:%d",
				mmr.PubIp.String(), mmr.PubPort)
			qryMgr.pubTcpIp = mmr.PubIp
			qryMgr.pubTcpPort = mmr.PubPort
			if eno := qryMgr.switch2NatAddr(proto); eno != DhtEnoNone {
				qryLog.Debug("natMakeMapRsp: switch2NatAddr failed, eno: %d", eno)
				return sch.SchEnoUserTask
			}
		} else {
			qryMgr.pubTcpIp = net.IPv4zero
			qryMgr.pubTcpPort = 0
		}
		_, ptrConMgr := qryMgr.sdl.SchGetUserTaskNode(ConMgrName)
		qryMgr.sdl.SchSetRecver(msg, ptrConMgr)
		return qryMgr.sdl.SchSendMessage(msg)
	}

	qryLog.Debug("natMakeMapRsp: unknown protocol reported: %s", proto)
	return sch.SchEnoParameter
}

//
// public address changed indication
//
func (qryMgr *QryMgr) natPubAddrUpdateInd(msg *sch.SchMessage) sch.SchErrno {
	// see comments in function tabMgrNatPubAddrUpdateInd for more please. to query manager,
	// when status from nat is bad, nothing to do but just backup the status.
	_, ptrConMgr := qryMgr.sdl.SchGetUserTaskNode(ConMgrName)
	qryMgr.sdl.SchSetRecver(msg, ptrConMgr)
	qryMgr.sdl.SchSendMessage(msg)

	ind := msg.Body.(*sch.MsgNatMgrPubAddrUpdateInd)
	proto := strings.ToLower(ind.Proto)
	if proto != nat.NATP_TCP {
		qryLog.Debug("natPubAddrUpdateInd: bad protocol: %s", proto)
		return sch.SchEnoParameter
	}

	oldResult := qryMgr.natTcpResult
	if qryMgr.natTcpResult = nat.NatIsStatusOk(ind.Status); !qryMgr.natTcpResult {
		qryLog.Debug("natPubAddrUpdateInd: result bad")
		return sch.SchEnoNone
	}

	qryLog.Debug("natPubAddrUpdateInd: proto: %s, old: %s:%d; new: %s:%d",
		ind.Proto, qryMgr.pubTcpIp.String(), qryMgr.pubTcpPort, ind.PubIp.String(), ind.PubPort)

	qryMgr.pubTcpIp = ind.PubIp
	qryMgr.pubTcpPort = ind.PubPort
	if !oldResult || !ind.PubIp.Equal(qryMgr.pubTcpIp) || ind.PubPort != qryMgr.pubTcpPort {
		qryLog.Debug("natPubAddrUpdateInd: call natMapSwitch")
		if eno := qryMgr.natMapSwitch(); eno != DhtEnoNone {
			qryLog.Debug("natPubAddrUpdateInd: natMapSwitch failed, error: %s", eno.Error())
			return sch.SchEnoUserTask
		}
	}
	return sch.SchEnoNone
}

//
// Get query manager configuration
//
func (qryMgr *QryMgr) qryMgrGetConfig() DhtErrno {
	cfg := config.P2pConfig4DhtQryManager(qryMgr.sdl.SchGetP2pCfgName())
	qmCfg := &qryMgr.qmCfg
	qmCfg.local = cfg.Local
	qmCfg.maxActInsts = cfg.MaxActInsts
	qmCfg.qryExpired = cfg.QryExpired
	qmCfg.qryInstExpired = cfg.QryInstExpired
	return DhtEnoNone
}

//
// Delete query control blcok from manager
//
const (
	delQcb4TargetFound      = iota // target had been found
	delQcb4NoMoreQueries           // no pendings and no actived instances
	delQcb4Timeout                 // query manager time out for the control block
	delQcb4Command                 // required by other module
	delQcb4NoSeeds                 // no seeds for query
	delQcb4TargetInLocal           // target found in local
	delQcb4QryInstDoneInd          // query instance done is indicated
	delQcb4QryInstResultInd        // query instance result indicated
	delQcb4InteralErrors           // internal errors while tring to query
	delQcb4PubAddrSwitch           // public address switching
)

func (qryMgr *QryMgr) qryMgrDelQcb(why int, target config.DsKey) DhtErrno {
	var strDebug = ""
	switch why {
	case delQcb4TargetFound:
		strDebug = "delQcb4TargetFound"
	case delQcb4NoMoreQueries:
		strDebug = "delQcb4NoMoreQueries"
	case delQcb4Timeout:
		strDebug = "delQcb4Timeout"
	case delQcb4NoSeeds:
		strDebug = "delQcb4NoSeeds"
	case delQcb4TargetInLocal:
		strDebug = "delQcb4TargetInLocal"
	case delQcb4QryInstDoneInd:
		strDebug = "delQcb4QryInstDoneInd"
	case delQcb4InteralErrors:
		strDebug = "delQcb4InteralErrors"
	case delQcb4PubAddrSwitch:
		strDebug = "delQcb4PubAddrSwitch"
	case delQcb4Command:
		strDebug = "delQcb4Command"
	default:
		qryLog.Debug("qryMgrDelQcb: parameters mismatched, why: %d", why)
		return DhtEnoMismatched
	}

	qryLog.Debug("qryMgrDelQcb: why: %s", strDebug)

	qcb, ok := qryMgr.qcbTab[target]
	if !ok {
		qryLog.Debug("qryMgrDelQcb: target not found: %x", target)
		return DhtEnoNotFound
	}

	if qcb.status != qsInited {
		delete(qryMgr.qcbTab, target)
		return DhtEnoNone
	}

	if qcb.qryTid != sch.SchInvalidTid {
		qryMgr.sdl.SchKillTimer(qryMgr.ptnMe, qcb.qryTid)
		qcb.qryTid = sch.SchInvalidTid
	}

	for _, icb := range qcb.qryActived {
		po := sch.SchMessage{}
		icb.sdl.SchMakeMessage(&po, qryMgr.ptnMe, icb.ptnInst, sch.EvSchPoweroff, nil)
		po.TgtName = icb.name
		icb.sdl.SchSendMessage(&po)
	}

	if qcb.rutNtfFlag == true {
		req := sch.MsgDhtRutMgrStopNofiyReq{
			Task:   qryMgr.ptnMe,
			Target: qcb.target,
		}
		msg := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&msg, qryMgr.ptnMe, qryMgr.ptnRutMgr, sch.EvDhtRutMgrStopNotifyReq, &req)
		qryMgr.sdl.SchSendMessage(&msg)
	}

	delete(qryMgr.qcbTab, target)
	return DhtEnoNone
}

//
// Delete query instance control block
//
func (qryMgr *QryMgr) qryMgrDelIcb(why int, target *config.DsKey, peer *config.NodeID) DhtErrno {

	qryLog.Debug("qryMgrDelIcb: why: %d", why)

	if why != delQcb4QryInstDoneInd && why != delQcb4QryInstResultInd {
		qryLog.Debug("qryMgrDelIcb: why delete?! why: %d", why)
		return DhtEnoMismatched
	}
	qcb, ok := qryMgr.qcbTab[*target]
	if !ok {
		qryLog.Debug("qryMgrDelIcb: target not found: %x", target)
		return DhtEnoNotFound
	}
	icb, ok := qcb.qryActived[*peer]
	if !ok {
		qryLog.Debug("qryMgrDelIcb: target not found: %x", target)
		return DhtEnoNotFound
	}

	qryLog.Debug("qryMgrDelIcb: icb: %s", icb.name)

	if why == delQcb4QryInstResultInd {
		eno, ptn := icb.sdl.SchGetUserTaskNode(icb.name)
		if eno == sch.SchEnoNone && ptn != nil && ptn == icb.ptnInst {
			po := sch.SchMessage{}
			icb.sdl.SchMakeMessage(&po, qryMgr.ptnMe, icb.ptnInst, sch.EvSchPoweroff, nil)
			po.TgtName = icb.name
			icb.sdl.SchSendMessage(&po)
		} else {
			qryLog.Debug("qryMgrDelIcb: not found, icb: %s", icb.name)
		}
	}
	delete(qcb.qryActived, *peer)
	return DhtEnoNone
}

//
// Update query result of query control block
//
func (qcb *qryCtrlBlock) qcbUpdateResult(qri *qryResultInfo) DhtErrno {
	li := qcb.qryResult
	for el := li.Front(); el != nil; el = el.Next() {
		v := el.Value.(*qryResultInfo)
		if qri.dist < v.dist {
			li.InsertBefore(qri, el)
			return DhtEnoNone
		}
	}
	li.PushBack(qri)
	return DhtEnoNone
}

//
// Put node to pending queue
//
func (qcb *qryCtrlBlock) qryMgrQcbPutPending(nodes []*qryPendingInfo, size int) DhtErrno {
	if len(nodes) == 0 || size <= 0 {
		qryLog.Debug("qryMgrQcbPutPending: no pendings to be put")
		return DhtEnoParameter
	}

	qryLog.Debug("qryMgrQcbPutPending: " +
		"number of nodes to be put: %d, size: %d", len(nodes), size)

	li := qcb.qryPending
	for _, n := range nodes {
		if _, dup := qcb.qryHistory[n.node.ID]; dup {
			qryLog.Debug("qryMgrQcbPutPending: duplicated, n: %+v", n)
			continue
		}
		pb := true
		for el := li.Front(); el != nil; el = el.Next() {
			v := el.Value.(*qryPendingInfo)
			if v.node.ID == n.node.ID {
				pb = false
				break
			}
			if n.dist < v.dist {
				li.InsertBefore(n, el)
				pb = false
				break
			}
		}
		if pb {
			qryLog.Debug("qryMgrQcbPutPending: PushBack, n: %+v", n)
			li.PushBack(n)
		}
	}

	for li.Len() > size {
		li.Remove(li.Back())
	}

	return DhtEnoNone
}

//
// Put node to actived queue and start query to the node
//
func (qryMgr *QryMgr) qryMgrQcbPutActived(qcb *qryCtrlBlock) (DhtErrno, int) {

	if qcb.qryPending == nil || qcb.qryPending.Len() == 0 {
		qryLog.Debug("qryMgrQcbPutActived: no pending")
		return DhtEnoNotFound, 0
	}

	if len(qcb.qryActived) >= qryMgr.qmCfg.maxActInsts {
		qryLog.Debug("qryMgrQcbPutActived: no room")
		return DhtEnoResource, 0
	}

	act := make([]*list.Element, 0)
	cnt := 0
	dhtEno := DhtEnoNone

	for el := qcb.qryPending.Front(); el != nil; el = el.Next() {
		if len(qcb.qryActived) >= qryMgr.qmCfg.maxActInsts {
			break
		}

		pending := el.Value.(*qryPendingInfo)
		act = append(act, el)

		if _, dup := qcb.qryActived[pending.node.ID]; dup == true {
			qryLog.Debug("qryMgrQcbPutActived: duplicated node: %X", pending.node.ID)
			continue
		}

		qryLog.Debug("qryMgrQcbPutActived: pending to be activated: %+v", *pending)

		icb := qryInstCtrlBlock{
			sdl:        qryMgr.sdl,
			seq:        qcb.icbSeq,
			qryReq:     qcb.qryReq,
			name:       "qryMgrIcb" + fmt.Sprintf("_q%d_i%d", qcb.seq, qcb.icbSeq),
			ptnInst:    nil,
			ptnConMgr:  nil,
			ptnRutMgr:  nil,
			ptnQryMgr:  nil,
			local:      qryMgr.qmCfg.local,
			status:     qisNull,
			target:     qcb.target,
			to:         pending.node,
			dir:        ConInstDirUnknown,
			qTid:       sch.SchInvalidTid,
			begTime:    time.Time{},
			endTime:    time.Time{},
			conBegTime: time.Time{},
			conEndTime: time.Time{},
			depth:      pending.depth,
		}

		qryLog.Debug("qryMgrQcbPutActived: ForWhat: %d", icb.qryReq.ForWhat)

		qryInst := NewQryInst()
		qryInst.icb = &icb
		td := sch.SchTaskDescription{
			Name:   icb.name,
			MbSize: sch.SchDftMbSize,
			Ep:     qryInst,
			Wd:     &sch.SchWatchDog{HaveDog: false},
			Flag:   sch.SchCreatedGo,
			DieCb:  nil,
			UserDa: &icb,
		}

		eno, ptn := qryMgr.sdl.SchCreateTask(&td)
		if eno != sch.SchEnoNone || ptn == nil {

			qryLog.Debug("qryMgrQcbPutActived: " +
				"SchCreateTask failed, eno: %d", eno)

			dhtEno = DhtEnoScheduler
			break
		}

		qcb.qryActived[icb.to.ID] = &icb
		qcb.qryHistory[icb.to.ID] = pending
		cnt++

		icb.ptnInst = ptn
		qcb.icbSeq++

		po := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&po, qryMgr.ptnMe, ptn, sch.EvSchPoweron, nil)
		qryMgr.sdl.SchSendMessage(&po)

		start := sch.SchMessage{}
		qryMgr.sdl.SchMakeMessage(&start, qryMgr.ptnMe, ptn, sch.EvDhtQryInstStartReq, nil)
		qryMgr.sdl.SchSendMessage(&start)

		qryLog.Debug("qryMgrQcbPutActived: icb: %s, EvSchPoweron and EvDhtQryInstStartReq sent", icb.name)
	}

	for _, el := range act {
		qcb.qryPending.Remove(el)
	}

	qryLog.Debug("qryMgrQcbPutActived: " +
		"pending: %d, actived: %d, history: %d",
		qcb.qryPending.Len(), len(qcb.qryActived), len(qcb.qryHistory))

	return DhtErrno(dhtEno), cnt
}

//
// Start timer for query control block
//
func (qryMgr *QryMgr) qryMgrQcbStartTimer(qcb *qryCtrlBlock) DhtErrno {
	td := sch.TimerDescription{
		Name:  "qryMgrQcbTimer" + fmt.Sprintf("%d", qcb.seq),
		Utid:  sch.DhtQryMgrQcbTimerId,
		Tmt:   sch.SchTmTypeAbsolute,
		Dur:   qryMgr.qmCfg.qryExpired,
		Extra: qcb,
	}
	tid := sch.SchInvalidTid
	eno := sch.SchEnoUnknown
	if eno, tid = qryMgr.sdl.SchSetTimer(qryMgr.ptnMe, &td); eno != sch.SchEnoNone || tid == sch.SchInvalidTid {
		qryLog.Debug("qryMgrQcbStartTimer: SchSetTimer failed, eno: %d", eno)
		return DhtEnoScheduler
	}
	qcb.qryTid = tid
	return DhtEnoNone
}

//
// Query control block timer handler
//
func (qryMgr *QryMgr) qcbTimerHandler(qcb *qryCtrlBlock) sch.SchErrno {
	qryMgr.qryMgrResultReport(qcb, DhtEnoTimeout.GetEno(), nil, nil, nil)
	qryMgr.qryMgrDelQcb(delQcb4Timeout, qcb.target)
	return sch.SchEnoNone
}

//
// Query result report
//
func (qryMgr *QryMgr) qryMgrResultReport(
	qcb *qryCtrlBlock,
	eno int,
	peer *config.Node,
	val []byte,
	prd *sch.Provider) DhtErrno {

	//
	// notice:
	//
	// 0) the target backup in qcb has it's meaning with "forWhat", as:
	//
	//		target						forWhat
	// =================================================
	//		peer identity				FIND_NODE
	//		key of value				GET_VALUE
	//		key of value				GET_PROVIDER
	//
	// 1) if eno indicated none of errors, then, the target is found, according to "forWhat",
	// one can obtain peer or value or provider from parameters passed in;
	//
	// 2) if eno indicated anything than EnoNone, then parameters "peer", "val", "prd" are
	// all be nil, and the peer(node) identities suggested to by query are backup in the
	// query result in "qcb";
	//
	// the event EvDhtQryMgrQueryResultInd handler should take the above into account to deal
	// with this event when it's received in the owner task of the "qcb".
	//
	// notice: "peer" passed in is not used, the "qcb.qryResult"
	//

	var ind = sch.MsgDhtQryMgrQueryResultInd{
		Eno:     eno,
		ForWhat: qcb.forWhat,
		Target:  qcb.target,
		Val:     val,
		Prds:    nil,
		Peers:   nil,
	}

	if prd != nil {
		ind.Prds = prd.Nodes
	}

	li := qcb.qryResult
	if li.Len() > 0 {
		idx := 0
		ind.Peers = make([]*config.Node, li.Len())
		for el := li.Front(); el != nil; el = el.Next() {
			v := el.Value.(*qryResultInfo)
			ind.Peers[idx] = &v.node
		}
	}

	qryLog.Debug("qryMgrResultReport: eno: %d, ForWhat: %d, task: %s",
		ind.Eno, ind.ForWhat, qryMgr.sdl.SchGetTaskName(qcb.ptnOwner))

	var msg = sch.SchMessage{}
	qryMgr.sdl.SchMakeMessage(&msg, qryMgr.ptnMe, qcb.ptnOwner, sch.EvDhtQryMgrQueryResultInd, &ind)
	qryMgr.sdl.SchSendMessage(&msg)
	return DhtEnoNone
}

//
// switch address to that reported from nat manager
//
func (qryMgr *QryMgr) switch2NatAddr(proto string) DhtErrno {
	if proto == nat.NATP_TCP {
		qryMgr.qmCfg.local.IP = qryMgr.pubTcpIp
		qryMgr.qmCfg.local.TCP = uint16(qryMgr.pubTcpPort & 0xffff)
		return DhtEnoNone
	}
	qryLog.Debug("switch2NatAddr: invalid protocol: %s", proto)
	return DhtEnoParameter
}

//
// start nat map switching procedure
//
func (qryMgr *QryMgr) natMapSwitch() DhtErrno {
	qryLog.Debug("natMapSwitch: entered")
	for k, qcb := range qryMgr.qcbTab {
		fw := qcb.forWhat
		ind := sch.MsgDhtQryMgrQueryResultInd{
			Eno:     DhtEnoNatMapping.GetEno(),
			ForWhat: fw,
			Target:  k,
			Peers:   make([]*config.Node, 0),
			Val:     make([]byte, 0),
			Prds:    make([]*config.Node, 0),
		}
		msg := sch.SchMessage{}
		if qcb.ptnOwner != nil {
			qryMgr.sdl.SchMakeMessage(&msg, qryMgr.ptnMe, qcb.ptnOwner, sch.EvDhtQryMgrQueryResultInd, &ind)
			qryMgr.sdl.SchSendMessage(&msg)
		}
		qryLog.Debug("natMapSwitch: EvDhtQryMgrQueryResultInd sent")

		if eno := qryMgr.qryMgrDelQcb(delQcb4PubAddrSwitch, k); eno != DhtEnoNone {
			qryLog.Debug("natMapSwitch: qryMgrDelQcb failed, eno: %d", eno)
			return eno
		}
		qryLog.Debug("natMapSwitch: qcb deleted, key: %x", k)
	}

	qryLog.Debug("natMapSwitch: call switch2NatAddr")
	qryMgr.switch2NatAddr(nat.NATP_TCP)

	msg := sch.SchMessage{}
	qryMgr.sdl.SchMakeMessage(&msg, qryMgr.ptnMe, qryMgr.ptnDhtMgr, sch.EvDhtQryMgrPubAddrSwitchInd, nil)
	qryMgr.sdl.SchSendMessage(&msg)
	qryLog.Debug("natMapSwitch: EvDhtQryMgrPubAddrSwitchInd sent")

	return DhtEnoNone
}

//
// get unique sequence number all query
//
var mapQrySeqLock = make(map[string]sync.Mutex, 0)

func GetQuerySeqNo(name string) int64 {
	qrySeqLock, ok := mapQrySeqLock[name]
	if !ok {
		panic("GetQuerySeqNo: internal error! seems system not ready")
	}
	qrySeqLock.Lock()
	defer qrySeqLock.Unlock()
	return time.Now().UnixNano()
}
