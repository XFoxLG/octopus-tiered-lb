package handlers

import (
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/server/router"
)

func TestDatabaseMigrationRoutesRetiredAndBackupRoutesRemain(test *testing.T) {
	const subprocessFlag = "OCTOPUS_TEST_DATABASE_RETIREMENT_ROUTES"
	if os.Getenv(subprocessFlag) != "1" {
		// RegisterAll consumes its registry. A fresh process preserves the
		// registrations for other handler tests, regardless of test order.
		executablePath, err := os.Executable()
		if err != nil {
			test.Fatalf("locate test executable: %v", err)
		}
		command := exec.Command(executablePath, "-test.run=^"+test.Name()+"$", "-test.count=1")
		command.Env = append(os.Environ(), subprocessFlag+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			test.Fatalf("check database route retirement: %v\n%s", err, output)
		}
		return
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		test.Fatalf("register routes: %v", err)
	}

	registeredRoutes := make(map[string]bool)
	for _, route := range engine.Routes() {
		registeredRoutes[route.Method+" "+route.Path] = true
		switch route.Path {
		case "/api/v1/setting/database/test", "/api/v1/setting/database/migrate":
			test.Errorf("retired database route remains registered: %s %s", route.Method, route.Path)
		}
	}

	for _, routeKey := range []string{
		http.MethodGet + " /api/v1/setting/export",
		http.MethodPost + " /api/v1/setting/import",
	} {
		if !registeredRoutes[routeKey] {
			test.Errorf("backup route is missing: %s", routeKey)
		}
	}
}
