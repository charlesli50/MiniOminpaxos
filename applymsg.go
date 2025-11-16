package omnipaxos

import (
	"time"
)

func (op *OmniPaxos) applymsg(applyCh chan ApplyMsg, currIdx int) {
	if op.killed() {
		return
	}
	op.mu.Lock()
	logLen := len(op.log)
	decidedIdx := op.decidedIdx
	op.mu.Unlock()

	if currIdx < decidedIdx && currIdx < logLen {
		op.mu.Lock()
		applyMsg := ApplyMsg{
			true,
			op.log[currIdx],
			currIdx,
		}
		op.mu.Unlock()
		applyCh <- applyMsg
		go op.applymsg(applyCh, currIdx+1)

	} else {
		time.Sleep(op.delay)
		go op.applymsg(applyCh, currIdx)
	}

}
