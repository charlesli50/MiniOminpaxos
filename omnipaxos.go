package omnipaxos

//
// This is an outline of the API that OmniPaxos must expose to
// the service (or tester). See comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   Create a new OmniPaxos server.
// op.Start(command interface{}) (index, ballot, isleader)
//   Start agreement on a new log entry
// op.GetState() (ballot, isLeader)
//   ask a OmniPaxos for its current ballot, and whether it thinks it is leader
// ApplyMsg
//   Each time a new entry is committed to the log, each OmniPaxos peer
//   should send an ApplyMsg to the service (or tester) in the same server.
//

import (
	"cmp"
	"omnipaxos/labrpc"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

type Phase int

const (
	PREPARE Phase = iota
	PROMISE
	ACCEPTSYNC
	ACCEPT
	DECIDE
	RECOVER
)

type BallotNumber struct {
	Value int
	Pid   int
}

// lexicographic comparison
func (bn BallotNumber) Compare(other BallotNumber) int {
	return cmp.Or(
		cmp.Compare(bn.Value, other.Value),
		cmp.Compare(bn.Pid, other.Pid),
	)
}

// promise comparison
func (pm Promise) Compare(other Promise) int {
	return cmp.Or(
		other.accRnd.Compare(pm.accRnd),
		cmp.Compare(other.logIdx, pm.logIdx),
	)
}

type Ballot struct {
	BallotNumber BallotNumber
	Qc           bool
}

type Promise struct {
	accRnd BallotNumber //bn
	logIdx int
	f      int
	decIdx int
	sfx    []any
}

type OmniPaxos struct {
	mu            sync.Mutex
	peers         []*labrpc.ClientEnd
	persister     *Persister
	me            int
	dead          int32
	enableLogging int32

	// all state described in paper:
	log         []any        //equal to []interface{}
	promisedRnd BallotNumber //bn
	acceptedRnd BallotNumber //bn
	decidedIdx  int

	// STATE
	role  Role
	phase Phase

	// leader info
	currentRnd BallotNumber    //bn
	promises   map[int]Promise // map from follower ID to their promise
	maxProm    Promise
	accepted   []int
	buffer     []any // client requests

	// ballot leader election algorithm state
	L       BallotNumber // ballot number of curr leader
	R       int          // current heartbeat round
	B       BallotNumber //ballot number. Initially set to (0, pid)
	qc      bool         // qourum connected
	delay   time.Duration
	ballots []Ballot

	//custom recovery mechanisms
	LinkDrop         bool
	LinkLastHBRound  int
	disconnectedRnds map[int]int // track consecutive rounds without heartbeat per peer

	// Semaphore for serializing RPC calls
	serializeCh chan struct{}
}

// As each OmniPaxos peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). Set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

// GetState Return the current leader's ballot and whether this server
// believes it is the leader.
func (op *OmniPaxos) GetState() (int, bool) {
	var ballot int
	var isleader bool

	// Your code here (3).
	op.mu.Lock()
	defer op.mu.Unlock()

	ballot = op.L.Value
	isleader = op.L.Pid == op.me

	return ballot, isleader
}

func (op *OmniPaxos) initOmniPaxos() {
	op.L = BallotNumber{-1, -1}
	op.R = 0
	op.B = BallotNumber{0, op.me}
	op.qc = true
	op.delay = 100 * time.Millisecond
	op.ballots = nil
	op.decidedIdx = -1
	op.LinkDrop = false
	op.LinkLastHBRound = 0
	op.disconnectedRnds = make(map[int]int)
	for i := range op.peers {
		op.disconnectedRnds[i] = 0
	}
}

func (op *OmniPaxos) startTimer(delay time.Duration) {
	if op.killed() {
		log.Warn().Msgf("startTimer(%v): got killed", op.R)
		return
	}

	op.mu.Lock()
	if op.R > op.LinkLastHBRound+3 {
		op.LinkDrop = true
	}
	// make buffered channel to store ballot results from heartbeats
	ballotsCh := make(chan Ballot, len(op.peers))
	op.mu.Unlock()

	// upon timeout of start timer
	timeoutCh := time.After(op.delay)
	wg := sync.WaitGroup{}

	op.sendHBRequest(&wg, ballotsCh, timeoutCh)

	<-timeoutCh
	wg.Wait()
	close(ballotsCh)
	op.mu.Lock()
	defer op.mu.Unlock()
	op.ballots = append(op.ballots, Ballot{op.B, op.qc})
	for ballot := range ballotsCh {
		op.ballots = append(op.ballots, ballot)
	}

	if len(op.ballots) > (len(op.peers) / 2) {
		op.checkLeader()
	} else {
		op.qc = false
	}

	// for each pair, check consecutive rounds of not receiving a heartbeat
	for pid := range op.peers {
		op.disconnectedRnds[pid] += 1
	}

	for _, ballot := range op.ballots {
		pid := ballot.BallotNumber.Pid
		if op.disconnectedRnds[pid] >= 4 {
			// we reconnected to pid after 3 consecutive no-heartbeat rounds
			op.reconnected(pid)
		}
		op.disconnectedRnds[pid] = 0
	}
	op.Debug("startTimer(%v): disconnectedRnds: %v", op.R, op.disconnectedRnds)

	// clear ballots
	op.ballots = nil
	op.R += 1

	// Continue timer if not killed
	go op.startTimer(delay)
}

// The service using OmniPaxos (e.g. a k/v server) wants to start
// agreement on the next command to be appended to OmniPaxos's log. If this
// server isn't the leader, returns false. Otherwise start the
// agreement and return immediately. There is no guarantee that this
// command will ever be committed to the OmniPaxos log, since the leader
// may fail or lose an election. Even if the OmniPaxos instance has been killed,
// this function should return gracefully.
//
// The first return value is the index that the command will appear at
// if it's ever committed. The second return value is the current
// ballot. The third return value is true if this server believes it is
// the leader.

// The tester doesn't halt goroutines created by OmniPaxos after each test,
// but it does call the Kill() method. Your code can use killed() to
// check whether Kill() has been called. The use of atomic avoids the
// need for a lock.
//
// The issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. Any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (op *OmniPaxos) Kill() {
	atomic.StoreInt32(&op.dead, 1)
	// Your code here, if desired.
	// you may set a variable to false to
	// disable logs as soon as a server is killed
	atomic.StoreInt32(&op.enableLogging, 0)
}

func (op *OmniPaxos) killed() bool {
	z := atomic.LoadInt32(&op.dead)
	return z == 1
}

// The service or tester wants to create a OmniPaxos server. The ports
// of all the OmniPaxos servers (including this one) are in peers[]. This
// server's port is peers[me]. All the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects OmniPaxos to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *OmniPaxos {

	op := &OmniPaxos{}
	op.peers = peers
	op.persister = persister
	op.me = me

	// Your initialization code here (3, 4).
	log.Info().Msgf("Hello from OmniPaxos!")
	op.initOmniPaxos()
	op.serializeCh = make(chan struct{}, 1) // buffered semaphore

	// start looking for leaders!
	go op.startTimer(op.delay)
	go op.applymsg(applyCh, 0)
	return op
}
