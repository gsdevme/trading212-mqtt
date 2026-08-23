package trading212

import (
	"encoding/json"
	"fmt"
	"time"
)

// The wire types below mirror the API's JSON exactly and are deliberately
// unexported: nothing outside this package should depend on the upstream shape.
// Mapping happens in one place so a change to the API is a change to one file.

type wireAccountSummary struct {
	ID       int64  `json:"id"`
	Currency string `json:"currency"`
	Cash     struct {
		AvailableToTrade  float64 `json:"availableToTrade"`
		InPies            float64 `json:"inPies"`
		ReservedForOrders float64 `json:"reservedForOrders"`
	} `json:"cash"`
	Investments struct {
		CurrentValue         float64 `json:"currentValue"`
		TotalCost            float64 `json:"totalCost"`
		UnrealizedProfitLoss float64 `json:"unrealizedProfitLoss"`
		RealizedProfitLoss   float64 `json:"realizedProfitLoss"`
	} `json:"investments"`
	TotalValue float64 `json:"totalValue"`
}

type wirePosition struct {
	Instrument struct {
		Ticker   string `json:"ticker"`
		Name     string `json:"name"`
		ISIN     string `json:"isin"`
		Currency string `json:"currency"`
	} `json:"instrument"`
	Quantity float64 `json:"quantity"`
	// QuantityAvailableForTrading is decoded for documentation of the API's
	// response shape but deliberately has no corresponding domain field:
	// nothing in the approved entity catalogue publishes it, and this
	// project's conventions are YAGNI. Do not add it to Position.
	QuantityAvailableForTrading float64   `json:"quantityAvailableForTrading"`
	QuantityInPies              float64   `json:"quantityInPies"`
	AveragePricePaid            float64   `json:"averagePricePaid"`
	CurrentPrice                float64   `json:"currentPrice"`
	CreatedAt                   time.Time `json:"createdAt"`
	WalletImpact                struct {
		Currency             string  `json:"currency"`
		CurrentValue         float64 `json:"currentValue"`
		TotalCost            float64 `json:"totalCost"`
		UnrealizedProfitLoss float64 `json:"unrealizedProfitLoss"`
		FXImpact             float64 `json:"fxImpact"`
	} `json:"walletImpact"`
}

// parseAccountSummary maps the account summary response into the domain type,
// stamping now as the observation time.
func parseAccountSummary(body []byte, now time.Time) (AccountSummary, error) {
	var w wireAccountSummary
	if err := json.Unmarshal(body, &w); err != nil {
		return AccountSummary{}, fmt.Errorf("decode account summary: %w", err)
	}
	return AccountSummary{
		ID:           w.ID,
		Currency:     w.Currency,
		TotalValue:   w.TotalValue,
		FreeCash:     w.Cash.AvailableToTrade,
		CashInPies:   w.Cash.InPies,
		CashReserved: w.Cash.ReservedForOrders,
		Invested:     w.Investments.TotalCost,
		CurrentValue: w.Investments.CurrentValue,
		UnrealizedPL: w.Investments.UnrealizedProfitLoss,
		RealizedPL:   w.Investments.RealizedProfitLoss,
		ReturnPct:    ReturnPct(w.Investments.UnrealizedProfitLoss, w.Investments.TotalCost),
		LastUpdated:  now,
	}, nil
}

// parsePositions maps the positions response into domain types.
//
// The two currencies are kept distinct: AvgPrice and CurrentPrice stay in the
// instrument's currency, while the wallet-impact figures stay in the account's.
// walletImpact.currency is authoritative for the latter; accountCurrency is the
// fallback for a response that omits it.
func parsePositions(body []byte, accountCurrency string) ([]Position, error) {
	var ws []wirePosition
	if err := json.Unmarshal(body, &ws); err != nil {
		return nil, fmt.Errorf("decode positions: %w", err)
	}
	out := make([]Position, 0, len(ws))
	for _, w := range ws {
		acct := w.WalletImpact.Currency
		if acct == "" {
			acct = accountCurrency
		}
		out = append(out, Position{
			Ticker:             w.Instrument.Ticker,
			Name:               w.Instrument.Name,
			ISIN:               w.Instrument.ISIN,
			InstrumentCurrency: w.Instrument.Currency,
			AccountCurrency:    acct,

			Quantity:       w.Quantity,
			QuantityInPies: w.QuantityInPies,

			AvgPrice:     w.AveragePricePaid,
			CurrentPrice: w.CurrentPrice,

			Value:        w.WalletImpact.CurrentValue,
			Cost:         w.WalletImpact.TotalCost,
			UnrealizedPL: w.WalletImpact.UnrealizedProfitLoss,
			FXImpact:     w.WalletImpact.FXImpact,

			ReturnPct: ReturnPct(w.WalletImpact.UnrealizedProfitLoss, w.WalletImpact.TotalCost),
			Opened:    w.CreatedAt,
		})
	}
	return out, nil
}
