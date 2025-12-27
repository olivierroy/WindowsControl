package main

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

const listenAddr = ":8181"

var pageTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Windows Control</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            height: 100vh;
            margin: 0;
            background: #f4f5f7;
        }
        .card {
            background: white;
            padding: 2.5rem;
            border-radius: 12px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.1);
            text-align: center;
        }
        .buttons {
            display: flex;
            flex-direction: column;
            gap: 0.75rem;
        }
        button {
            background: #c0392b;
            color: white;
            border: none;
            padding: 1rem 2rem;
            border-radius: 8px;
            font-size: 1.1rem;
            cursor: pointer;
            transition: background 0.2s ease;
        }
        button:hover:enabled { background: #e74c3c; }
        button:disabled { opacity: 0.5; cursor: not-allowed; }
        #status { margin-top: 1rem; font-weight: bold; }
    </style>
</head>
<body>
    <div class="card">
        <h1>Windows Power Control</h1>
        <p>Click the button below to shut down this machine immediately.</p>
        <div class="buttons">
            <button id="shutdown">Shut Down</button>
            <button id="restart">Restart</button>
            <button id="restart-bios">Restart to BIOS</button>
        </div>
        <div id="status"></div>
    </div>
    <script>
        const status = document.getElementById('status');
        const actions = [
            {
                id: 'shutdown',
                endpoint: '/shutdown',
                confirm: 'This will power off the machine right away. Continue?'
            },
            {
                id: 'restart',
                endpoint: '/restart',
                confirm: 'This will restart the machine immediately. Continue?'
            },
            {
                id: 'restart-bios',
                endpoint: '/restart-bios',
                confirm: 'This will restart straight into firmware/BIOS (UEFI systems only). Continue?'
            }
        ];

        actions.forEach(action => {
            const btn = document.getElementById(action.id);
            btn.addEventListener('click', async () => {
                if (!confirm(action.confirm)) {
                    return;
                }
                status.textContent = 'Sending command...';
                status.style.color = '#2c3e50';
                toggleButtons(true);
                try {
                    const response = await fetch(action.endpoint, { method: 'POST' });
                    const data = await response.json();
                    status.textContent = data.message;
                    status.style.color = response.ok ? '#2c3e50' : '#c0392b';
                } catch (err) {
                    status.textContent = 'Failed to contact server.';
                    status.style.color = '#c0392b';
                } finally {
                    toggleButtons(false);
                }
            });
        });

        function toggleButtons(disabled) {
            actions.forEach(action => {
                document.getElementById(action.id).disabled = disabled;
            });
        }
    </script>
</body>
</html>`))

func main() {
	handled, err := maybeRunService()
	if err != nil {
		log.Fatalf("service initialization failed: %v", err)
	}
	if handled {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runHTTPServer(ctx); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func runHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := pageTemplate.Execute(w, nil); err != nil {
			log.Printf("render template: %v", err)
		}
	})
	mux.HandleFunc("/shutdown", shutdownHandler)
	mux.HandleFunc("/restart", restartHandler)
	mux.HandleFunc("/restart-bios", restartFirmwareHandler)

	srv := &http.Server{Addr: listenAddr, Handler: logRequests(mux)}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("graceful shutdown error: %v", err)
		}
	}()

	log.Printf("Windows control web server listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	handlePowerAction(w, r, []string{"/s", "/t", "0"}, "Shutdown command executed. The machine is powering off.")
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	handlePowerAction(w, r, []string{"/r", "/t", "0"}, "Restart command executed. The machine is restarting.")
}

func restartFirmwareHandler(w http.ResponseWriter, r *http.Request) {
	handlePowerAction(w, r, []string{"/r", "/fw", "/t", "0"}, "Firmware restart command executed. The machine will reboot into BIOS/UEFI.")
}

func handlePowerAction(w http.ResponseWriter, r *http.Request, args []string, successMessage string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if runtime.GOOS != "windows" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"message": "Power control commands are available only on Windows hosts.",
		})
		return
	}

	cmd := exec.Command("shutdown", args...)
	if err := cmd.Run(); err != nil {
		log.Printf("power command failed (%v): %v", args, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"message": "Failed to execute power command.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": successMessage,
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
