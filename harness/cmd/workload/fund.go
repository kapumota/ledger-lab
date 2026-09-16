package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// fondearTodas acredita cada cuenta de cliente desde la cuenta de capital,
// usando el mismo contrato HTTP que la campana. No se escribe directamente en
// la base a proposito. Si el arnes pudiera escribir por otra ruta, las cifras
// de la campana dejarian de ser atribuibles al sistema bajo prueba.
func fondearTodas(baseURL, moneda, fondeador string, cuentas []string, monto int64) error {
	cliente := &http.Client{Timeout: 30 * time.Second}

	for i, c := range cuentas {
		key, err := claveAleatoria()
		if err != nil {
			return err
		}
		cuerpo, _ := json.Marshal(map[string]any{
			"idempotency_key": key,
			"currency":        moneda,
			"postings": []map[string]any{
				{"account_id": fondeador, "amount_minor": -monto},
				{"account_id": c, "amount_minor": monto},
			},
		})
		resp, err := cliente.Post(baseURL+"/entries", "application/json", bytes.NewReader(cuerpo))
		if err != nil {
			return fmt.Errorf("cuenta %d: %w", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			return fmt.Errorf("cuenta %d: el fondeo devolvio %d", i, resp.StatusCode)
		}
	}
	return nil
}

func claveAleatoria() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
