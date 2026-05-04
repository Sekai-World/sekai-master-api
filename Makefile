MISE ?= mise

.EXPORT_ALL_VARIABLES:

MISE_TASKS := \
	run \
	dev \
	dev-down \
	dev-logs \
	test \
	test-docker \
	tidy \
	format \
	lint \
	swagger \
	migrate-up \
	migrate-down \
	dev-env-up \
	dev-env-down \
	dev-env-down-purge \
	dev-env-logs \
	keycloak-token \
	smoke \
	admin-open \
	dev-logs-ui

.PHONY: $(MISE_TASKS)

$(MISE_TASKS):
	$(MISE) run $@
