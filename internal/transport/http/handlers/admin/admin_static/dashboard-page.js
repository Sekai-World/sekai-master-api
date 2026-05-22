import { initDashboardPage } from "./dashboard.js";

const swaggerDebugLink = document.getElementById("swagger-debug-link");

if (swaggerDebugLink) {
	try {
		const docsResponse = await fetch("/docs/openapi.json", { method: "GET" });
		if (docsResponse.ok) {
			swaggerDebugLink.hidden = false;
		}
	} catch {
	}
}

await initDashboardPage();
