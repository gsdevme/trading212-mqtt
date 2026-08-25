# Changelog

## [0.2.0](https://github.com/gsdevme/trading212-mqtt/compare/v0.1.0...v0.2.0) (2026-08-25)


### Features

* **cmd:** wire the serve pipeline and add the dump diagnostic ([126555d](https://github.com/gsdevme/trading212-mqtt/commit/126555d06bcbb49d68ff639c8a12ac6dcf47fff3))
* **homeassistant:** add entity catalogues and discovery payloads ([871a111](https://github.com/gsdevme/trading212-mqtt/commit/871a11136852d39a835904fbda5fa6620c5a75bb))
* **homeassistant:** round return_pct to 2dp in Home Assistant ([4951951](https://github.com/gsdevme/trading212-mqtt/commit/49519510267d38d0f0406239188c886ce1773beb))
* **mock:** add fake Trading 212 API and mock subcommand ([b367fa4](https://github.com/gsdevme/trading212-mqtt/commit/b367fa4176696859642455345091574f01243154))
* **mqtt:** add autopaho backend with LWT and reconnect callback ([967ce26](https://github.com/gsdevme/trading212-mqtt/commit/967ce269296a227f7943611d8401e8be0f1ba74a))
* **publisher:** publish account and position state with availability lifecycle ([a308742](https://github.com/gsdevme/trading212-mqtt/commit/a3087421b18142730a54d074712273989fe0ad7e))
* **scheduler:** add poll loop with retry and health reporting ([d2e3ecb](https://github.com/gsdevme/trading212-mqtt/commit/d2e3ecb66492681ec1dafb34706020de6773c596))
* **scheduler:** log held tickers and list them on the status page ([cfde35d](https://github.com/gsdevme/trading212-mqtt/commit/cfde35dc2a702b7d9d474e737f93ff46a96f039b))
* **server:** add status page and health probes ([484d516](https://github.com/gsdevme/trading212-mqtt/commit/484d516d05043facf8874e53c37684ccef6e62dc))
* **trading212:** add domain model, whitelist and ticker slug ([dd40f91](https://github.com/gsdevme/trading212-mqtt/commit/dd40f915fad1c2196b501eba656eac92c88229be))
* **trading212:** add per-endpoint rate limiter with 429 backoff ([125fd83](https://github.com/gsdevme/trading212-mqtt/commit/125fd833f531dab098b87bd74fd51ae30a6e5530))
* **trading212:** add read-only API client with auth and error mapping ([9576df8](https://github.com/gsdevme/trading212-mqtt/commit/9576df81aa5c2dab6dc96996d0fc08bebe363f47))
* **trading212:** parse account summary and positions from the API ([ab9a997](https://github.com/gsdevme/trading212-mqtt/commit/ab9a997b12a2c805c1d6fad15a9e8594b1dadd25))


### Bug Fixes

* **cmd:** honour SIGTERM while connecting to MQTT ([4cae22b](https://github.com/gsdevme/trading212-mqtt/commit/4cae22b0e41e28815350fbf9ce8a803125ffd096))
* **config:** redact MQTT_PASSWORD, validate broker scheme and integers ([7aaf262](https://github.com/gsdevme/trading212-mqtt/commit/7aaf262c2b36da6aba65c19fa7008674d397731d))
* **publisher:** force availability publishes on shutdown ([6272488](https://github.com/gsdevme/trading212-mqtt/commit/62724888888da4693e2e7889fee96fb938b1c840))
* **publisher:** republish current state on reconnect, not startup ([8b18554](https://github.com/gsdevme/trading212-mqtt/commit/8b185542599a15d1baed37d1123c2195938b1949))
* **specs:** resolve startup-order contradiction and config gaps ([866734f](https://github.com/gsdevme/trading212-mqtt/commit/866734fae3b79ccf9de288082c21bb95b9fb2428))
* **trading212:** reconcile domain-model spec drift, add Snapshot tags ([e437f04](https://github.com/gsdevme/trading212-mqtt/commit/e437f04894cb92f42594cdc5a78bc1f55547fb56))
