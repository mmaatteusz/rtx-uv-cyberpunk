package main

import (
	"errors"
	"time"
)

const watchdogInterval = 2 * time.Second
const watchdogMaxAge = 12 * time.Second

func checkWatchdogSignal(done <-chan struct{}, modified, now time.Time) error {
	if done == nil {
		return errors.New("brak uchwytu do procesu nadzoru")
	}
	select {
	case <-done:
		return errors.New("proces nadzoru zakończył działanie")
	default:
	}
	age := now.Sub(modified)
	if modified.IsZero() || age > watchdogMaxAge || age < -watchdogInterval {
		return errors.New("brak aktualnego sygnału z procesu nadzoru")
	}
	return nil
}
