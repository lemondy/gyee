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
	config	"github.com/yeeco/gyee/p2p/config"
	sch 	"github.com/yeeco/gyee/p2p/scheduler"
	dcv		"github.com/yeeco/gyee/p2p/discover"
	tab		"github.com/yeeco/gyee/p2p/discover/table"
	ngb		"github.com/yeeco/gyee/p2p/discover/neighbor"
			"github.com/yeeco/gyee/p2p/peer"
			"github.com/yeeco/gyee/p2p/dht"
	dhtro	"github.com/yeeco/gyee/p2p/dht/router"
	dhtch	"github.com/yeeco/gyee/p2p/dht/chunker"
	dhtre	"github.com/yeeco/gyee/p2p/dht/retriver"
	dhtst	"github.com/yeeco/gyee/p2p/dht/storer"
	dhtsy	"github.com/yeeco/gyee/p2p/dht/syncer"
	log		"github.com/yeeco/gyee/p2p/logger"
)

//
// watch dog is not implemented
//
var noDog = sch.SchWatchDog {
	HaveDog:false,
}

//
// Create description about static tasks
//
func P2pCreateStaticTaskTab() []sch.TaskStaticDescription {

	return []sch.TaskStaticDescription {

		//
		// Following are static tasks for ycp2p module internal. Notice that fields of struct
		// sch.TaskStaticDescription like MbSize, Wd, Flag will be set to default values internal
		// scheduler, please see function schimplSchedulerStart for details pls.
		//

		{	Name:dcv.DcvMgrName,		Tep:dcv.NewDcvMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:tab.TabMgrName,		Tep:tab.NewTabMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:tab.NdbcName,			Tep:tab.NewNdbCleaner(),	MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:ngb.LsnMgrName,		Tep:ngb.NewLsnMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:ngb.NgbMgrName,		Tep:ngb.NewNgbMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:peer.PeerLsnMgrName,	Tep:peer.NewLsnMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:peer.PeerMgrName,		Tep:peer.NewPeerMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},

		{	Name:dht.DhtMgrName,		Tep:dht.NewDhtMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:dhtro.DhtroMgrName,	Tep:dhtro.NewDhtrMgr(),		MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:dhtch.DhtchMgrName,	Tep:dhtch.NewDhtchMgr(),	MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:dhtre.DhtreMgrName,	Tep:dhtre.NewDhtreMgr(),	MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:dhtst.DhtstMgrName,	Tep:dhtst.NewDhtstMgr(),	MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
		{	Name:dhtsy.DhtsyMgrName,	Tep:dhtsy.NewDhtsyMgr(),	MbSize:-1,	DieCb: nil,		Wd:noDog,	Flag:sch.SchCreatedSuspend},
	}
}

//
// Poweron order of static user tasks
//
var TaskStaticPoweronOrder = []string {
	dcv.DcvMgrName,
	tab.TabMgrName,
	tab.NdbcName,
	ngb.LsnMgrName,
	ngb.NgbMgrName,
	peer.PeerMgrName,
	peer.PeerLsnMgrName,
	dht.DhtMgrName,
	dhtro.DhtroMgrName,
	dhtch.DhtchMgrName,
	dhtre.DhtreMgrName,
	dhtst.DhtstMgrName,
	dhtsy.DhtsyMgrName,
}

//
// Append a static user task to table TaskStaticTab
//
func AppendStaticTasks(
	tab		[]sch.TaskStaticDescription,
	name	string,
	tep		sch.SchUserTaskInf,
	dcb		func(interface{})sch.SchErrno,
	dog		sch.SchWatchDog) sch.SchErrno {

	log.LogCallerFileLine("AppendStaticTasks: " +
		"static task would be appended: \n" +
		"name: %s\n" +
		"tep: %p\n" +
		"dcb: %p\n" +
		"dog: %+v",
		name,
		tep,
		dcb,
		dog)

	tab = append(tab, sch.TaskStaticDescription{Name:name, Tep:tep, DieCb:dcb, Wd:dog})

	return sch.SchEnoNone
}

//
// Init p2p
//
func P2pCreateInstance(cfg *config.Config) (*sch.Scheduler, sch.SchErrno) {
	return sch.SchSchedulerInit(cfg)
}

//
// Start p2p
//
func P2pStart(sdl *sch.Scheduler) (sch.SchErrno) {

	//
	// Start all static tasks
	//

	var eno sch.SchErrno

	eno, _ = sdl.SchSchedulerStart(P2pCreateStaticTaskTab(), TaskStaticPoweronOrder)

	if eno != sch.SchEnoNone {
		return eno
	}

	//
	// Check peer manager init result, would be blocked until its init
	// procedure ended.
	//

	var pmEno peer.PeMgrErrno

	peMgr := sdl.SchGetUserTaskIF(sch.PeerMgrName).(*peer.PeerManager)
	pmEno = peMgr.PeMgrInited()

	if pmEno != peer.PeMgrEnoNone {
		return sch.SchEnoUserTask
	}

	//
	// Startup peer manager
	//

	pmEno = peMgr.PeMgrStart()

	if pmEno != peer.PeMgrEnoNone {

		log.LogCallerFileLine("P2pStart: " +
			"PeMgrStart failed, eno: %d",
			pmEno)

		return sch.SchEnoUserTask
	}

	return sch.SchEnoNone
}





