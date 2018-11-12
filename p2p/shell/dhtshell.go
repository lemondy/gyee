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

package shell

import (
	log "github.com/ethereum/go-ethereum/log"
	sch "github.com/yeeco/gyee/p2p/scheduler"
	dht "github.com/yeeco/gyee/p2p/dht"
)


const (
	dhtShMgrName = sch.DhtShMgrName						// name registered in scheduler
	dhtShEvQueueSize = 64								// event indication queue size
	dhtShCsQueueSize = 64								// connection status indication queue size
)

type dhtShellManager struct {
	sdl				*sch.Scheduler						// pointer to scheduler
	name			string								// my name
	tep				sch.SchUserTaskEp					// task entry
	ptnMe			interface{}							// pointer to task node of myself
	ptnDhtMgr		interface{}							// pointer to dht manager task node
	evChan			chan *sch.MsgDhtShEventInd			// event indication channel
	csChan			chan *sch.MsgDhtConInstStatusInd	// connection status indication channel
}

//
// Create dht shell manager
//
func NewDhtShellMgr() *dhtShellManager {
	shMgr := dhtShellManager {
		name: dhtShMgrName,
		evChan: make(chan *sch.MsgDhtShEventInd, dhtShEvQueueSize),
		csChan: make(chan *sch.MsgDhtConInstStatusInd, dhtShCsQueueSize),
	}
	shMgr.tep = shMgr.shMgrProc
	return &shMgr
}

//
// Entry point exported to scheduler
//
func (shMgr *dhtShellManager)TaskProc4Scheduler(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {
	return shMgr.tep(ptn, msg)
}

//
// Shell manager entry
//
func (shMgr *dhtShellManager)shMgrProc(ptn interface{}, msg *sch.SchMessage) sch.SchErrno {

	eno := sch.SchEnoUnknown

	switch msg.Id {
	case sch.EvSchPoweron:
		eno = shMgr.poweron(ptn)

	case sch.EvSchPoweroff:
		eno = shMgr.poweroff(ptn)

	case sch.EvDhtShEventInd:
		eno = shMgr.dhtShEventInd(msg.Body.(*sch.MsgDhtShEventInd))

	case sch.EvDhtMgrFindPeerReq:
		eno = shMgr.dhtShFindPeerReq(msg.Body.(*sch.MsgDhtQryMgrQueryStartReq))

	case sch.EvDhtBlindConnectReq:
		eno = shMgr.dhtShBlindConnectReq(msg.Body.(*sch.MsgDhtBlindConnectReq))

	case sch.EvDhtMgrGetProviderReq:
		eno = shMgr.dhtShGetProviderReq(msg.Body.(*sch.MsgDhtMgrGetProviderReq))

	case sch.EvDhtMgrPutProviderReq:
		eno = shMgr.dhtShPutProviderReq(msg.Body.(*sch.MsgDhtPrdMgrAddProviderReq))

	default:
		log.Debug("shMgrProc: unknown event: %d", msg.Id)
		eno = sch.SchEnoParameter
	}

	return eno
}

func (shMgr *dhtShellManager)poweron(ptn interface{}) sch.SchErrno {
	var eno sch.SchErrno
	shMgr.ptnMe = ptn
	shMgr.sdl = sch.SchGetScheduler(ptn)
	if eno, shMgr.ptnDhtMgr = shMgr.sdl.SchGetTaskNodeByName(sch.DhtMgrName); eno != sch.SchEnoNone {
		log.Debug("poweron: dht manager task not found")
		return eno
	}
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)poweroff(ptn interface{}) sch.SchErrno {
	log.Debug("poweroff: task will be done...")
	close(shMgr.evChan)
	close(shMgr.csChan)
	return shMgr.sdl.SchTaskDone(shMgr.ptnMe, sch.SchEnoPowerOff)
}

func (shMgr *dhtShellManager)dhtShEventInd(ind *sch.MsgDhtShEventInd) sch.SchErrno {

	evt := ind.Evt
	msg := ind.Msg
	eno := sch.SchEnoUnknown

	switch evt {

	case  sch.EvDhtBlindConnectRsp:
		eno = shMgr.dhtBlindConnectRsp(msg.(*sch.MsgDhtBlindConnectRsp))

	case  sch.EvDhtMgrFindPeerRsp:
		eno = shMgr.dhtMgrFindPeerRsp(msg.(*sch.MsgDhtQryMgrQueryResultInd))

	case  sch.EvDhtQryMgrQueryStartRsp:
		eno = shMgr.dhtQryMgrQueryStartRsp(msg.(*sch.MsgDhtQryMgrQueryStartRsp))

	case  sch.EvDhtQryMgrQueryStopRsp:
		eno = shMgr.dhtQryMgrQueryStopRsp(msg.(*sch.MsgDhtQryMgrQueryStopRsp))

	case  sch.EvDhtConMgrSendCfm:
		eno = shMgr.dhtConMgrSendCfm(msg.(*sch.MsgDhtConMgrSendCfm))

	case  sch.EvDhtMgrPutProviderRsp:
		eno = shMgr.dhtMgrPutProviderRsp(msg.(*sch.MsgDhtPrdMgrAddProviderRsp))

	case  sch.EvDhtMgrGetProviderRsp:
		eno = shMgr.dhtMgrGetProviderRsp(msg.(*sch.MsgDhtMgrGetProviderRsp))

	case  sch.EvDhtMgrPutValueRsp:
		eno = shMgr.dhtMgrPutValueRsp(msg.(*sch.MsgDhtMgrPutValueRsp))

	case  sch.EvDhtMgrGetValueRsp:
		eno = shMgr.dhtMgrGetValueRsp(msg.(*sch.MsgDhtMgrGetValueRsp))

	case  sch.EvDhtConMgrCloseRsp:
		eno = shMgr.dhtConMgrCloseRsp(msg.(*sch.MsgDhtConMgrCloseRsp))

	case  sch.EvDhtConInstStatusInd:
		eno = shMgr.dhtConInstStatusInd(msg.(*sch.MsgDhtConInstStatusInd))
		return eno

	default:
		log.Debug("dhtTestEventCallback: unknown event type: %d", evt)
		return sch.SchEnoParameter
	}

	if eno == sch.SchEnoNone {
		shMgr.evChan<-ind
	}

	return eno
}

func (shMgr *dhtShellManager)dhtBlindConnectRsp(msg *sch.MsgDhtBlindConnectRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtMgrFindPeerRsp(msg *sch.MsgDhtQryMgrQueryResultInd) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtQryMgrQueryStartRsp(msg *sch.MsgDhtQryMgrQueryStartRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtQryMgrQueryStopRsp(msg *sch.MsgDhtQryMgrQueryStopRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtConMgrSendCfm(msg *sch.MsgDhtConMgrSendCfm) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtMgrPutProviderRsp(msg *sch.MsgDhtPrdMgrAddProviderRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtMgrGetProviderRsp(msg *sch.MsgDhtMgrGetProviderRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtMgrPutValueRsp(msg *sch.MsgDhtMgrPutValueRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtMgrGetValueRsp(msg *sch.MsgDhtMgrGetValueRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtConMgrCloseRsp(msg *sch.MsgDhtConMgrCloseRsp) sch.SchErrno {
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtConInstStatusInd(msg *sch.MsgDhtConInstStatusInd) sch.SchErrno {

	switch msg.Status {

	case dht.CisNull:
		log.Debug("dhtConInstStatusInd: CisNull")

	case dht.CisConnecting:
		log.Debug("dhtConInstStatusInd: CisConnecting")

	case dht.CisConnected:
		log.Debug("dhtConInstStatusInd: CisConnected")

	case dht.CisAccepted:
		log.Debug("dhtTestConInstStatusInd: CisAccepted")

	case dht.CisInHandshaking:
		log.Debug("dhtTestConInstStatusInd: CisInHandshaking")

	case dht.CisHandshaked:
		log.Debug("dhtTestConInstStatusInd: CisHandshaked")

	case dht.CisInService:
		log.Debug("dhtTestConInstStatusInd: CisInService")

	case dht.CisClosed:
		log.Debug("dhtTestConInstStatusInd: CisClosed")

	default:
		log.Debug("dhtTestConInstStatusInd: unknown status: %d", msg.Status)
		return sch.SchEnoParameter
	}

	shMgr.csChan<-msg
	return sch.SchEnoNone
}

func (shMgr *dhtShellManager)dhtShFindPeerReq(req *sch.MsgDhtQryMgrQueryStartReq) sch.SchErrno {
	msg := sch.SchMessage{}
	shMgr.sdl.SchMakeMessage(&msg, shMgr.ptnMe, shMgr.ptnDhtMgr, sch.EvDhtMgrFindPeerReq, req)
	return shMgr.sdl.SchSendMessage(&msg)
}

func (shMgr *dhtShellManager)dhtShBlindConnectReq(req *sch.MsgDhtBlindConnectReq) sch.SchErrno {
	msg := sch.SchMessage{}
	shMgr.sdl.SchMakeMessage(&msg, shMgr.ptnMe, shMgr.ptnDhtMgr, sch.EvDhtBlindConnectReq, req)
	return shMgr.sdl.SchSendMessage(&msg)
}

func (shMgr *dhtShellManager)dhtShGetProviderReq(req *sch.MsgDhtMgrGetProviderReq) sch.SchErrno {
	msg := sch.SchMessage{}
	shMgr.sdl.SchMakeMessage(&msg, shMgr.ptnMe, shMgr.ptnDhtMgr, sch.EvDhtMgrGetProviderReq, req)
	return shMgr.sdl.SchSendMessage(&msg)
}

func (shMgr *dhtShellManager)dhtShPutProviderReq(req *sch.MsgDhtPrdMgrAddProviderReq) sch.SchErrno {
	msg := sch.SchMessage{}
	shMgr.sdl.SchMakeMessage(&msg, shMgr.ptnMe, shMgr.ptnDhtMgr, sch.EvDhtMgrPutProviderReq, req)
	return shMgr.sdl.SchSendMessage(&msg)
}

func (schMgr *dhtShellManager)GetEventChan() chan *sch.MsgDhtShEventInd {
	return schMgr.evChan
}

func (schMgr *dhtShellManager)GetConnStatusChan() chan *sch.MsgDhtConInstStatusInd {
	return schMgr.csChan
}