package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
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
			"bang!": "1.0.0-alpha.1",
		})
	})
	app.Post("/new", func(c fiber.Ctx) error {
		redirect := c.Queries()["url"]
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

		err = client.Do(ctx, client.B().Rpush().Key(slug).Element(redirect).Element(key).Build()).Error()
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

		return c.Redirect().To(val)
	})
	app.Delete("/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		key := c.Queries()["key"]
		if slug == "" || slug == "!" {
			return fiber.NewError(fiber.StatusBadRequest, "missing slug")
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

		return c.SendString(fmt.Sprintf("deleted redirect for %v", val))
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
	defer client.Close()

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
