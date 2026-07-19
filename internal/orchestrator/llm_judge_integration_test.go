// +build integration

package orchestrator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
)

// TestJudgeWithFreerideProxy runs the Judge validation tests against the freeride proxy.
// Requires: freeride proxy running on localhost:11434 (start with: go run ./cmd/freeride proxy --port 11434)
// Skips if proxy not available.
func TestJudgeWithFreerideProxy(t *testing.T) {
	if os.Getenv("GASTOWN_TEST_FREERIDE") != "1" {
		t.Skip("Set GASTOWN_TEST_FREERIDE=1 to run Judge tests against freeride proxy")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := llm.NewClient(
		"http://localhost:11434/v1/chat/completions",
		"deepseek/deepseek-v4-flash",
		"",
		30*time.Second,
	)

	// First verify proxy is reachable
	if err := client.Ping(ctx); err != nil {
		t.Skipf("freeride proxy not reachable at localhost:11434: %v", err)
	}

	// Test 1: ValidateDocumentWithJudge
	t.Run("ValidateDocumentWithJudge", func(t *testing.T) {
		pass, reason, err := ValidateDocumentWithJudge(ctx, client, JudgeConfig{
			DocumentName: "test doc",
			Content: `# Test Architecture

## Docker & Deployment
Multi-stage build: Stage 1 uses node:20-slim to build frontend. Stage 2 uses python:3.12-slim to install backend deps. Exposes port 8000. CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000"].

## Integration and testing
We use pytest for unit tests and Playwright for E2E tests. Run with docker-compose -f test/docker-compose.test.yml up --build.`,
			Criteria: []string{
				"Documents the base images used in the Dockerfile",
				"Describes the multi-stage build steps clearly",
				"Specifies the exposed port (e.g., 8000)",
				"Documents the CMD/entrypoint used to run the server",
			},
			MinLength: 200,
		})
		if err != nil {
			t.Logf("Judge error (may be LLM unavailable): %v", err)
			return
		}
		if !pass {
			t.Logf("Judge rejected: %s", reason)
		} else {
			t.Logf("Judge passed: %s", reason)
		}
	})

	// Test 2: Triad validation
	t.Run("ValidateTriadWithJudge", func(t *testing.T) {
		spec := `# SPEC
| GET | /api/health | 200 | Health check |
| POST | /api/users | 201 | Create user |
`
		arch := `# Architecture

## HTTP API
| GET | /api/health | 200 | Health check |
| POST | /api/users | 201 | Create user |

## Integration and testing
Run unit tests with pytest. Integration tests use testcontainers.
`
		plan := `# Plan
## Integration contract
Entrypoint main.py wires dependencies. Registers GET /api/health and POST /api/users.

## Bead map
### fi-1: main.py
`
		pass, reason, err := ValidateTriadWithJudge(ctx, client, TriadValidationConfig{
			SPEC:         spec,
			Architecture: arch,
			Plan:         plan,
			MinLength:    100,
		})
		if err != nil {
			t.Logf("Triad judge error (may be LLM unavailable): %v", err)
			return
		}
		if !pass {
			t.Logf("Triad judge rejected: %s", reason)
		} else {
			t.Logf("Triad judge passed: %s", reason)
		}
	})

	// Test 3: Test quality
	t.Run("TestQualityJudge", func(t *testing.T) {
		pass, reason, err := ValidateTestQualityWithJudge(ctx, client, TestQualityConfig{
			TestFileContent: `"""Tests for portfolio trade execution and P&L calculation."""

import pytest
from datetime import datetime
from decimal import Decimal

from backend.db.models import OrderRequest
from backend.portfolio.engine import (
    validate_order,
    execute_trade,
    calculate_pnl,
    InsufficientFundsError,
    InsufficientSharesError,
    InvalidOrderError,
)


class TestValidateOrder:
    def test_valid_buy_passes(self):
        """A valid buy order does not raise."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="buy")
        validate_order(order, {}, 10000.0, 150.0)

    def test_valid_sell_passes(self):
        """A valid sell order does not raise."""
        order = OrderRequest(ticker="AAPL", quantity=5, side="sell")
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 140.0}}
        validate_order(order, holdings, 0.0, 150.0)

    def test_invalid_side_raises(self):
        """An order with invalid side raises InvalidOrderError."""
        order = OrderRequest(ticker="AAPL", quantity=1, side="hold")
        with pytest.raises(InvalidOrderError, match="Invalid side"):
            validate_order(order, {}, 10000.0, 150.0)

    def test_insufficient_funds_buy_raises(self):
        """A buy order exceeding available cash raises InsufficientFundsError."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="buy")
        with pytest.raises(InsufficientFundsError):
            validate_order(order, {}, 100.0, 150.0)

    def test_insufficient_funds_exact_check(self):
        """InsufficientFundsError has correct required and available."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="buy")
        try:
            validate_order(order, {}, 100.0, 150.0)
        except InsufficientFundsError as e:
            assert e.required == 1500.0
            assert e.available == 100.0

    def test_insufficient_shares_sell_raises(self):
        """A sell order exceeding held shares raises InsufficientSharesError."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="sell")
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 5, "cost_basis": 140.0}}
        with pytest.raises(InsufficientSharesError):
            validate_order(order, holdings, 0.0, 150.0)

    def test_insufficient_shares_exact_check(self):
        """InsufficientSharesError has correct ticker, requested, held."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="sell")
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 5, "cost_basis": 140.0}}
        try:
            validate_order(order, holdings, 0.0, 150.0)
        except InsufficientSharesError as e:
            assert e.ticker == "AAPL"
            assert e.requested == 10
            assert e.held == 5

    def test_sell_no_holdings_raises(self):
        """Selling a ticker not held raises InsufficientSharesError."""
        order = OrderRequest(ticker="AAPL", quantity=1, side="sell")
        with pytest.raises(InsufficientSharesError):
            validate_order(order, {}, 0.0, 150.0)


class TestExecuteTrade:
    def test_buy_adds_holdings(self):
        """Buy adds shares to holdings and deducts cash."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="buy")
        holdings, cash, record = execute_trade(order, {}, 10000.0, 150.0)
        assert holdings["AAPL"]["shares"] == 10
        assert cash == 10000.0 - 1500.0
        assert record.ticker == "AAPL"
        assert record.volume == 10

    def test_sell_removes_holdings(self):
        """Sell removes shares from holdings and adds cash."""
        order = OrderRequest(ticker="AAPL", quantity=5, side="sell")
        initial_holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 140.0}}
        holdings, cash, record = execute_trade(order, initial_holdings, 0.0, 150.0)
        assert holdings["AAPL"]["shares"] == 5
        assert cash == 750.0  # 5 * 150
        assert record.volume == 5

    def test_sell_removes_ticker_when_zero(self):
        """Sell removing all shares deletes the ticker from holdings."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="sell")
        initial_holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 140.0}}
        holdings, cash, record = execute_trade(order, initial_holdings, 0.0, 150.0)
        assert "AAPL" not in holdings
        assert cash == 1500.0

    def test_buy_updates_cost_basis(self):
        """Buy updates average cost basis for existing holdings."""
        order = OrderRequest(ticker="AAPL", quantity=5, side="buy")
        initial_holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 100.0}}
        holdings, cash, record = execute_trade(order, initial_holdings, 2000.0, 200.0)
        # total cost: 10*100 + 5*200 = 1000 + 1000 = 2000, shares: 15, avg: 2000/15 ≈ 133.33
        expected_basis = 2000.0 / 15
        assert holdings["AAPL"]["cost_basis"] == pytest.approx(expected_basis, rel=1e-2)
        assert holdings["AAPL"]["shares"] == 15

    def test_trade_record_created(self):
        """execute_trade creates a TradeRecord with correct fields."""
        order = OrderRequest(ticker="AAPL", quantity=10, side="buy")
        holdings, cash, record = execute_trade(order, {}, 10000.0, 150.0)
        assert record.ticker == "AAPL"
        assert record.price == 150.0
        assert record.volume == 10
        assert isinstance(record.timestamp, datetime)


class TestCalculatePnl:
    def test_unrealized_pnl_positive(self):
        """Unrealized P&L is positive when current price > cost basis."""
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 100.0}}
        prices = {"AAPL": 150.0}
        total, realized, unrealized = calculate_pnl(holdings, prices)
        assert unrealized == 500.0  # (150 - 100) * 10
        assert total == 500.0

    def test_unrealized_pnl_negative(self):
        """Unrealized P&L is negative when current price < cost basis."""
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 100.0}}
        prices = {"AAPL": 80.0}
        total, realized, unrealized = calculate_pnl(holdings, prices)
        assert unrealized == -200.0  # (80 - 100) * 10
        assert total == -200.0

    def test_unrealized_pnl_zero(self):
        """Unrealized P&L is zero when current price equals cost basis."""
        holdings = {"AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 100.0}}
        prices = {"AAPL": 100.0}
        total, realized, unrealized = calculate_pnl(holdings, prices)
        assert unrealized == 0.0
        assert total == 0.0

    def test_empty_holdings(self):
        """P&L is zero for empty holdings."""
        total, realized, unrealized = calculate_pnl({}, {"AAPL": 150.0})
        assert total == 0.0
        assert realized == 0.0
        assert unrealized == 0.0

    def test_multiple_holdings(self):
        """P&L sums across multiple holdings."""
        holdings = {
            "AAPL": {"ticker": "AAPL", "shares": 10, "cost_basis": 100.0},
            "GOOG": {"ticker": "GOOG", "shares": 5, "cost_basis": 2000.0},
        }
        prices = {"AAPL": 150.0, "GOOG": 2100.0}
        total, realized, unrealized = calculate_pnl(holdings, prices)
        assert unrealized == 1000.0
        assert total == 1000.0
`,
			SpecSection: `# SPEC
## Portfolio management
- Users can buy/sell shares
- Cash balance tracked
- P&L calculated`,
			ArchSection: `# Architecture
## Portfolio module
Handles trade execution and P&L calculation.`,
			FilePath:  "backend/tests/test_portfolio.py",
			MinLength: 200,
		})
		if err != nil {
			t.Logf("Test quality judge error: %v", err)
			return
		}
		if !pass {
			t.Logf("Test quality judge rejected: %s", reason)
		} else {
			t.Logf("Test quality judge passed: %s", reason)
		}
	})
}