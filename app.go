/*
 * Copyright © 2024 Oliver Pirger <0x4f48@proton.me>
 *
 * This program is free software: you can redistribute it and/or modify it under the terms of
 * the GNU General Public License, version 3, as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY;
 * without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
 * See the GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License along with this program.
 * If not, see <https://www.gnu.org/licenses/>.
 *
 * SPDX-License-Identifier: GPL-3.0-only
 */

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"

	"github.com/gofiber/fiber/v3"
	"github.com/sugawarayuuta/sonnet"
	"github.com/valkey-io/valkey-go"
)

func main() {
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"127.0.0.1:6379"}})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	app := fiber.New(fiber.Config{
		JSONEncoder: sonnet.Marshal,
		JSONDecoder: sonnet.Unmarshal,
	})

	app.Get("/", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"bang!": "1.1.0",
		})
	})
	app.Post("/new", func(c fiber.Ctx) error {
		redirect := c.Query("url")
		if redirect == "" {
			return fiber.NewError(fiber.StatusBadRequest, "missing url query parameter")
		}

		if !ValidateUrl(redirect) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid url, please add http:// or https://")
		}

		slug, err := RandStr(5)
		slug = fmt.Sprintf("!%v", slug)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to generate random string")
		}

		key, err := RandStr(64)

		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to generate admin key")
		}

		err = client.Do(ctx, client.B().Rpush().Key(slug).Element(redirect).Element(key).Element(fmt.Sprint(0)).Build()).Error()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to register redirect")
		}

		return c.JSON(fiber.Map{
			"slug": slug,
			"key":  key,
		})
	})
	app.Get("/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		if slug == "" || slug == "!" {
			return fiber.NewError(fiber.StatusBadRequest, "missing slug")
		}

		val, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(0).Build()).ToString()
		if err != nil || val == "" {
			return fiber.NewError(fiber.StatusBadRequest, "failed to retrieve redirect")
		}

		go func() {
			val, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(2).Build()).ToString()
			if err != nil {
				log.Println("Failed to get clicks counter for" + slug)
				return
			}
			valint, _ := strconv.Atoi(val)

			err = client.Do(ctx, client.B().Lset().Key(slug).Index(2).Element(fmt.Sprint(valint+1)).Build()).Error()
			if err != nil {
				log.Println("Failed to increment counter for" + slug)
				return
			}
		}()

		return c.Redirect().To(val)
	})
	app.Get("/clicks/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		key := c.Query("key")
		if slug == "" || slug == "!" {
			return fiber.NewError(fiber.StatusBadRequest, "missing slug")
		}
		if key == "" {
			return fiber.NewError(fiber.StatusBadRequest, "missing key from query params")
		}

		field, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(1).Build()).ToString()
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "failed to check admin key")
		}
		if field != key {
			return fiber.NewError(fiber.StatusUnauthorized)
		}

		val, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(2).Build()).ToString()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to get statistics")
		}

		return c.SendString(val)
	})
	app.Delete("/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		key := c.Query("key")
		if slug == "" || slug == "!" {
			return fiber.NewError(fiber.StatusBadRequest, "missing slug")
		}
		if key == "" {
			return fiber.NewError(fiber.StatusBadRequest, "missing key from query params")
		}

		val, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(0).Build()).ToString()
		if err != nil || val == "" {
			return fiber.NewError(fiber.StatusBadRequest, "failed to retrieve redirect")
		}

		field, err := client.Do(ctx, client.B().Lindex().Key(slug).Index(1).Build()).ToString()
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "failed to check admin key")
		}

		if field != key {
			return fiber.NewError(fiber.StatusUnauthorized)
		}

		err = client.Do(ctx, client.B().Del().Key(slug).Build()).Error()
		if err != nil || val == "" {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to delete redirect")
		}

		return c.SendStatus(fiber.StatusOK)
	})

	go func() {
		if err := app.Listen(":8080"); err != nil {
			log.Panic(err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	_ = <-c

	log.Println("Shutting down...")
	_ = app.Shutdown()

	log.Println("Cleaning up...")
	client.Close()

	log.Println("Successful shutdown.")
}

const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandStr(n int) (string, error) {
	bytes := make([]byte, n)

	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	for i, b := range bytes {
		bytes[i] = chars[b%byte(len(chars))]
	}

	return string(bytes), nil
}

func ValidateUrl(str string) bool {
	r, _ := regexp.Compile(`^http:\/\/[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$|^https:\/\/[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	return r.MatchString(str)
}
