Feature: Publish Trading 212 account and position metrics to MQTT

  The service reads the (mock) Trading 212 API and republishes account totals and
  whitelisted positions to MQTT with Home Assistant autodiscovery.

  Background:
    Given a GBP account holding AAPL_US_EQ, VUSA_EQ and FREE_EQ

  Scenario: Publish account discovery and availability on startup
    When the service starts up
    Then the account total value discovery config is published retained
    And the account device has no via_device
    And availability "online" is published retained

  Scenario: Publish account totals from a poll
    When the service starts up
    And a poll runs
    Then the account state reports total_value 15234.56
    And the account state reports free_cash 1200
    And the readiness endpoint reports ready

  Scenario: Track only whitelisted positions
    Given the tracked tickers are "AAPL_US_EQ"
    When the service starts up
    And a poll runs
    Then a state document is published for "AAPL_US_EQ"
    And no state document is published for "VUSA_EQ"

  Scenario: Track every position when the whitelist is a star
    Given the tracked tickers are "*"
    When the service starts up
    And a poll runs
    Then a state document is published for "AAPL_US_EQ"
    And a state document is published for "VUSA_EQ"

  Scenario: A position device nests under the account device
    Given the tracked tickers are "AAPL_US_EQ"
    When the service starts up
    And a poll runs
    Then the "AAPL_US_EQ" device is linked to the account device

  Scenario: Price entities use the instrument currency and value entities the account currency
    Given the tracked tickers are "AAPL_US_EQ"
    When the service starts up
    And a poll runs
    Then the "AAPL_US_EQ" entity "current_price" is denominated in "USD"
    And the "AAPL_US_EQ" entity "value" is denominated in "GBP"

  Scenario: A whitelisted ticker that is not held is greyed out
    Given the tracked tickers are "NOTHELD_EQ"
    When the service starts up
    Then availability "offline" is published for "NOTHELD_EQ"
    And a discovery config exists for "NOTHELD_EQ"

  Scenario: Selling out of a position marks it offline without removing it
    Given the tracked tickers are "AAPL_US_EQ"
    When the service starts up
    And a poll runs
    And the account no longer holds "AAPL_US_EQ"
    And a poll runs
    Then availability "offline" is published for "AAPL_US_EQ"
    And a discovery config exists for "AAPL_US_EQ"

  Scenario: A zero-cost position reports an unknown return
    Given the tracked tickers are "FREE_EQ"
    When the service starts up
    And a poll runs
    Then the "FREE_EQ" state reports a null return_pct

  Scenario: Readiness drops when the API keeps failing
    When the service starts up
    And a poll runs
    Then the readiness endpoint reports ready
    When the API starts failing
    And 3 polls run
    Then the readiness endpoint reports not ready

  Scenario: Retained state survives a failed poll
    When the service starts up
    And a poll runs
    And the account total value changes to 99999
    And the API starts failing
    And a poll runs
    Then the account state reports total_value 15234.56
