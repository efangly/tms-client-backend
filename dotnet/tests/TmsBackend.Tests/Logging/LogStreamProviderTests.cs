using Microsoft.Extensions.Logging;
using TmsBackend.Logging;

namespace TmsBackend.Tests.Logging;

public class LogStreamProviderTests
{
    private static readonly DateTime At = new(2026, 10, 26, 9, 30, 0, 123);

    [Fact]
    public void Keeps_information_and_above_with_short_category()
    {
        var p = new LogStreamProvider(now: () => At);
        var log = p.CreateLogger("TmsBackend.Services.PollingService");

        log.LogDebug("hidden");
        log.LogInformation("Polled {Count} probes", 3);
        log.LogError(new InvalidOperationException("boom"), "Save failed");

        var entries = p.Recent();
        Assert.Equal(2, entries.Count);
        Assert.Equal(new LogEntry(1, At, LogLevel.Information, "PollingService", "Polled 3 probes", null), entries[0]);
        Assert.Equal(2, entries[1].Id);
        Assert.Equal(LogLevel.Error, entries[1].Level);
        Assert.Contains("boom", entries[1].Exception);
    }

    [Fact]
    public void Ring_keeps_the_newest_entries()
    {
        var p = new LogStreamProvider(capacity: 3);
        var log = p.CreateLogger("x");
        for (var i = 1; i <= 5; i++)
            log.LogInformation("m{I}", i);

        Assert.Equal(["m3", "m4", "m5"], p.Recent().Select(e => e.Message));
        Assert.Equal(["m5"], p.Recent(afterId: 4).Select(e => e.Message));
        Assert.Empty(p.Recent(afterId: 5));
    }

    [Fact]
    public void Subscriber_gets_history_then_live_entries_without_overlap()
    {
        var p = new LogStreamProvider();
        var log = p.CreateLogger("x");
        log.LogInformation("before");

        var (history, live) = p.Subscribe();
        log.LogWarning("after");

        Assert.Equal(["before"], history.Select(e => e.Message));
        Assert.True(live.Reader.TryRead(out var e));
        Assert.Equal("after", e.Message);
        Assert.False(live.Reader.TryRead(out _));

        p.Unsubscribe(live);
        Assert.Equal(0, p.SubscriberCount);
        Assert.True(live.Reader.Completion.IsCompleted);
    }

    [Fact]
    public void Slow_subscriber_drops_oldest_without_blocking()
    {
        var p = new LogStreamProvider();
        var log = p.CreateLogger("x");
        var (_, live) = p.Subscribe();

        for (var i = 0; i < LogStreamProvider.SubscriberBuffer + 10; i++)
            log.LogInformation("m{I}", i);

        Assert.True(live.Reader.TryRead(out var first));
        Assert.Equal(11, first.Id);
    }
}
