/*
 * Copyright (C) 2021 The Zion Authors
 * This file is part of The Zion library.
 *
 * The Zion is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The Zion is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with The Zion.  If not, see <http://www.gnu.org/licenses/>.
 */

package core

import (
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/hotstuff"
)

var once sync.Once

// Start implements core.Engine.Start
func (c *core) Start(chain consensus.ChainReader) error {
	once.Do(func() {
		hotstuff.RegisterMsgTypeConvertHandler(func(data interface{}) hotstuff.MsgType {
			code := data.(uint64)
			return MsgType(code)
		})
	})

	c.isRunning = true
	c.backlogs = newBackLog()
	c.current = nil

	// Start a new round from last sequence + 1
	c.startNewRound(common.Big0)

	// Tests will handle events itself, so we have to make subscribeEvents()
	// be able to call in test.
	c.subscribeEvents()
	go c.handleEvents()
	return nil
}

// Stop implements core.Engine.Stop
func (c *core) Stop() error {
	c.stopTimer()
	c.unsubscribeEvents()
	c.isRunning = false
	return nil
}

// Address implement core.Engine.Address
func (c *core) Address() common.Address {
	return c.signer.Address()
}

// IsProposer implement core.Engine.IsProposer
func (c *core) IsProposer() bool {
	return c.valSet.IsProposer(c.backend.Address())
}

// IsCurrentProposal implement core.Engine.IsCurrentProposal
func (c *core) IsCurrentProposal(blockHash common.Hash) bool {
	if c.current == nil {
		return false
	}
	if proposal := c.current.Proposal(); proposal != nil && proposal.Hash() == blockHash {
		return true
	}
	if req := c.current.PendingRequest(); req != nil && req.Proposal != nil && req.Proposal.Hash() == blockHash {
		return true
	}
	return false
}

// ----------------------------------------------------------------------------

// Subscribe both internal and external events
func (c *core) subscribeEvents() {
	c.events = c.backend.EventMux().Subscribe(
		// external events
		hotstuff.RequestEvent{},
		hotstuff.MessageEvent{},
		// internal events
		backlogEvent{},
	)
	c.timeoutSub = c.backend.EventMux().Subscribe(
		timeoutEvent{},
	)
	c.finalCommittedSub = c.backend.EventMux().Subscribe(
		hotstuff.FinalCommittedEvent{},
	)
}

// Unsubscribe all events
func (c *core) unsubscribeEvents() {
	c.events.Unsubscribe()
	c.timeoutSub.Unsubscribe()
	c.finalCommittedSub.Unsubscribe()
}

func (c *core) handleEvents() {
	logger := c.logger.New("handleEvents", "state", c.currentState())

	for {
		select {
		case event, ok := <-c.events.Chan():
			if !ok {
				logger.Error("Failed to receive msg Event", "err", "subscribe event chan out empty")
				return
			}
			// A real Event arrived, process interesting content
			switch ev := event.Data.(type) {
			case hotstuff.RequestEvent:
				c.handleRequest(&hotstuff.Request{Proposal: ev.Proposal})

			case hotstuff.MessageEvent:
				c.handleMsg(ev.Payload)

			case backlogEvent:
				c.handleCheckedMsg(ev.msg, ev.src)
			}

		case _, ok := <-c.timeoutSub.Chan():
			if !ok {
				logger.Error("Failed to receive timeout Event")
				return
			}
			c.handleTimeoutMsg()

		case evt, ok := <-c.finalCommittedSub.Chan():
			if !ok {
				logger.Error("Failed to receive finalCommitted Event")
				return
			}
			switch ev := evt.Data.(type) {
			case hotstuff.FinalCommittedEvent:
				c.handleFinalCommitted(ev.Header)
			}
		}
	}
}

// sendEvent sends events to mux
func (c *core) sendEvent(ev interface{}) {
	c.backend.EventMux().Post(ev)
}

func (c *core) handleMsg(payload []byte) error {
	logger := c.logger.New()

	// Decode Message and check its signature
	msg := new(hotstuff.Message)
	if err := msg.FromPayload(payload, c.validateFn); err != nil {
		logger.Error("Failed to decode Message from payload", "err", err)
		return err
	}

	// Only accept Message if the address is valid
	_, src := c.valSet.GetByAddress(msg.Address)
	if src == nil {
		logger.Error("Invalid address in Message", "msg", msg)
		return errInvalidSigner
	}

	// handle checked Message
	if err := c.handleCheckedMsg(msg, src); err != nil {
		return err
	}
	return nil
}

func (c *core) handleCheckedMsg(msg *hotstuff.Message, src hotstuff.Validator) (err error) {
	switch msg.Code {
	case MsgTypeNewView:
		err = c.handleNewView(msg, src)
	case MsgTypePrepare:
		err = c.handlePrepare(msg, src)
	case MsgTypePrepareVote:
		err = c.handlePrepareVote(msg, src)
	case MsgTypePreCommit:
		err = c.handlePreCommit(msg, src)
	case MsgTypePreCommitVote:
		err = c.handlePreCommitVote(msg, src)
	case MsgTypeCommit:
		err = c.handleCommit(msg, src)
	case MsgTypeCommitVote:
		err = c.handleCommitVote(msg, src)
	case MsgTypeDecide:
		err = c.handleDecide(msg, src)
	default:
		err = errInvalidMessage
		c.logger.Error("msg type invalid", "unknown type", msg.Code)
	}

	if err == errFutureMessage {
		c.storeBacklog(msg, src)
	}
	return
}

func (c *core) handleTimeoutMsg() {
	c.logger.Trace("handleTimeout", "state", c.currentState(), "view", c.currentView())
	round := new(big.Int).Add(c.current.Round(), common.Big1)
	c.startNewRound(round)
}
