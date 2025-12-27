package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
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
		.delay-control {
			width: 100%;
			margin: 1.5rem 0 0.5rem;
			text-align: left;
		}
		.delay-control label {
			display: block;
			margin-bottom: 0.35rem;
			font-weight: bold;
			color: #2c3e50;
		}
		.delay-presets {
			display: flex;
			flex-wrap: wrap;
			gap: 0.5rem;
		}
		.delay-presets button {
			background: #ecf0f1;
			color: #2c3e50;
			border: 1px solid #d5d8dc;
			padding: 0.35rem 0.75rem;
			border-radius: 6px;
			cursor: pointer;
			font-size: 0.95rem;
		}
		.delay-presets button.selected {
			background: #2c3e50;
			color: white;
			border-color: #2c3e50;
		}
		.custom-delay {
			margin-top: 0.75rem;
		}
		.custom-delay input {
			width: 100%;
			padding: 0.5rem;
			border-radius: 6px;
			border: 1px solid #d5d8dc;
			font-size: 1rem;
			box-sizing: border-box;
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
		<p>Trigger these power actions immediately or schedule them shortly in the future.</p>
		<div class="delay-control">
			<label>Delay before running command</label>
			<div class="delay-presets" id="delay-presets">
				<button type="button" class="selected" data-delay-seconds="0">Immediately</button>
				<button type="button" data-delay-seconds="30">30 seconds</button>
				<button type="button" data-delay-seconds="300">5 minutes</button>
				<button type="button" data-delay-seconds="1800">30 minutes</button>
				<button type="button" data-delay-seconds="7200">2 hours</button>
			</div>
			<div class="custom-delay">
				<label for="delay-minutes">Or enter minutes</label>
				<input type="number" id="delay-minutes" min="0" placeholder="e.g. 10" />
			</div>
		</div>
        <div class="buttons">
            <button id="shutdown">Shut Down</button>
            <button id="restart">Restart</button>
            <button id="restart-bios">Restart to BIOS</button>
        </div>
        <div id="status"></div>
    </div>
    <script>
	const status = document.getElementById('status');
	const delayPresets = Array.from(document.querySelectorAll('#delay-presets button'));
	const delayMinutesInput = document.getElementById('delay-minutes');
	let selectedDelaySeconds = 0;

	delayPresets.forEach(btn => {
		btn.addEventListener('click', () => {
			selectedDelaySeconds = Number.parseInt(btn.dataset.delaySeconds, 10) || 0;
			delayPresets.forEach(b => b.classList.toggle('selected', b === btn));
			delayMinutesInput.value = '';
		});
	});

	delayMinutesInput.addEventListener('input', () => {
		const minutes = Number.parseFloat(delayMinutesInput.value);
		if (Number.isFinite(minutes) && minutes > 0) {
			selectedDelaySeconds = Math.round(minutes * 60);
		} else {
			selectedDelaySeconds = 0;
		}
		delayPresets.forEach(btn => btn.classList.remove('selected'));
	});

	const actions = [
		{
			id: 'shutdown',
			endpoint: '/shutdown',
			confirm: 'This will power off the machine using the selected delay. Continue?'
		},
		{
			id: 'restart',
			endpoint: '/restart',
			confirm: 'This will restart the machine using the selected delay. Continue?'
		},
		{
			id: 'restart-bios',
			endpoint: '/restart-bios',
			confirm: 'This will restart straight into firmware/BIOS (UEFI systems only) using the selected delay. Continue?'
		}
	];

        actions.forEach(action => {
            const btn = document.getElementById(action.id);
            btn.addEventListener('click', async () => {
                if (!confirm(action.confirm)) {
                    return;
                }
			const delaySeconds = selectedDelaySeconds;
                status.textContent = 'Sending command...';
                status.style.color = '#2c3e50';
                toggleButtons(true);
                try {
                    const response = await fetch(action.endpoint, {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json'
                        },
                        body: JSON.stringify({ delaySeconds })
                    });
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
		delayPresets.forEach(btn => btn.disabled = disabled);
		delayMinutesInput.disabled = disabled;
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
	handlePowerAction(w, r, []string{"/s"}, "Shutdown command staged. The machine is powering off.")
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	handlePowerAction(w, r, []string{"/r"}, "Restart command staged. The machine is restarting.")
}

func restartFirmwareHandler(w http.ResponseWriter, r *http.Request) {
	handlePowerAction(w, r, []string{"/r", "/fw"}, "Firmware restart command staged. The machine will reboot into BIOS/UEFI.")
}

func handlePowerAction(w http.ResponseWriter, r *http.Request, baseArgs []string, successMessage string) {
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

	delaySeconds, err := parseDelay(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"message": err.Error(),
		})
		return
	}

	args := append([]string{}, baseArgs...)
	args = append(args, "/t", strconv.Itoa(delaySeconds))
	cmd := exec.Command("shutdown", args...)
	if err := cmd.Run(); err != nil {
		log.Printf("power command failed (%v): %v", args, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"message": "Failed to execute power command.",
		})
		return
	}

	message := successMessage
	if delaySeconds > 0 {
		delay := time.Duration(delaySeconds) * time.Second
		message = fmt.Sprintf("%s It will run in %s.", successMessage, delay.Round(time.Second))
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": message,
	})
}

func parseDelay(r *http.Request) (int, error) {
	if r.Body == nil {
		return 0, nil
	}
	defer r.Body.Close()
	var payload struct {
		DelaySeconds int `json:"delaySeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		return 0, fmt.Errorf("invalid request body: %w", err)
	}
	if payload.DelaySeconds < 0 {
		return 0, errors.New("delaySeconds must be zero or positive")
	}
	return payload.DelaySeconds, nil
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
