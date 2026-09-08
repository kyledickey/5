package generallogging

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// benchmarkCacheWorkload precomputes gateway-shaped inputs outside timing. Every
// message has 256 ASCII content bytes and two attachments with 200-byte URLs. IDs
// vary across 850 guilds; no SQL, JSON decoding, Discord traffic or ID formatting
// is included in the measured loop. The cache uses its real 64 MiB byte budget.
func benchmarkCacheWorkload(b *testing.B, perGuild int) (*MessageCache, []CachedMessage) {
	b.Helper()
	cache := NewMessageCache(1000)
	messages := make([]CachedMessage, 0, 850*perGuild)
	content := strings.Repeat("x", 256)
	url := "https://cdn.discordapp.com/" + strings.Repeat("x", 174)
	for position := 0; position < perGuild; position++ {
		for guild := 0; guild < 850; guild++ {
			message := CachedMessage{GuildID: fmt.Sprintf("guild-%04d", guild), MessageDiscordID: fmt.Sprintf("message-%04d-%04d", guild, position), ChannelDiscordID: "channel-000000000001", AuthorDiscordUserID: "member-000000000001", Content: content, Attachments: []AttachmentMetadata{{DiscordID: "file-00000000000001", Filename: "proof-one.png", ContentType: "image/png", Size: 1024, URL: url}, {DiscordID: "file-00000000000002", Filename: "proof-two.png", ContentType: "image/png", Size: 1024, URL: url}}}
			messages = append(messages, message)
			cache.SetGuildLimit(message.GuildID, 1000)
			cache.Put(message)
		}
	}
	return cache, messages
}

// reportBenchmarkCache reports estimated retained bytes, not live Go heap size.
func reportBenchmarkCache(b *testing.B, cache *MessageCache) {
	b.Helper()
	b.ReportMetric(float64(cache.retainedBytes), "accounted-bytes")
	b.ReportMetric(float64(cache.global.Len()), "retained-messages")
	b.ReportMetric(float64(len(cache.guilds)), "retained-guilds")
	if cache.retainedBytes > cache.byteBudget {
		b.Fatal("cache exceeded accounting bound")
	}
}

// BenchmarkCache850Guilds compares steady replacement, shared global eviction,
// and mutex contention. One operation is SetGuildLimit plus Replace; the mixed
// parallel workload additionally performs a defensive Get on every fourth op.
func BenchmarkCache850Guilds(b *testing.B) {
	for _, perGuild := range []int{16, 128} {
		b.Run(fmt.Sprintf("replace-%d-per-guild", perGuild), func(b *testing.B) {
			cache, messages := benchmarkCacheWorkload(b, perGuild)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				message := messages[i%len(messages)]
				cache.SetGuildLimit(message.GuildID, 1000)
				cache.Replace(message)
			}
			b.StopTimer()
			reportBenchmarkCache(b, cache)
		})
	}
	b.Run("parallel-mixed-16-per-guild", func(b *testing.B) {
		cache, messages := benchmarkCacheWorkload(b, 16)
		var workers atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := int(workers.Add(1)) * 997
			for pb.Next() {
				message := messages[i%len(messages)]
				cache.SetGuildLimit(message.GuildID, 1000)
				cache.Replace(message)
				if i%4 == 0 {
					cache.Get(message.GuildID, message.MessageDiscordID)
				}
				i++
			}
		})
		b.StopTimer()
		reportBenchmarkCache(b, cache)
	})
}
