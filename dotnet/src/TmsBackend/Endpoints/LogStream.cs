using System.Globalization;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Http.Features;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Logging;

namespace TmsBackend.Endpoints;

/// <summary>
/// GET /api/log-stream: the app's log as Server-Sent Events (not in the Go
/// build). Sends "connected", then up to <c>history</c> recent entries, then
/// each new entry as it is logged, with a comment heartbeat like
/// /api/temperature-stream. Query: <c>level</c> = info (default) | warning |
/// error | critical, <c>history</c> = 0..buffer size (default 100). Every log
/// event carries its id, so EventSource resumes from Last-Event-ID after a
/// reconnect without repeating or skipping buffered entries.
/// </summary>
public static class LogStream
{
    public const int DefaultHistory = 100;

    public static async Task HandleAsync(HttpContext ctx, LogStreamProvider logs, IHostApplicationLifetime lifetime, TimeProvider time)
    {
        var req = ctx.Request;
        var res = ctx.Response;

        if (!TryParseLevel(req.Query["level"].ToString(), out var minLevel))
        {
            await Results.Json(new { error = "level must be info, warning, error or critical" }, statusCode: 400).ExecuteAsync(ctx);
            return;
        }
        var historyText = req.Query["history"].ToString();
        var history = DefaultHistory;
        if (historyText != "" && (!int.TryParse(historyText, NumberStyles.None, CultureInfo.InvariantCulture, out history) || history > logs.Capacity))
        {
            await Results.Json(new { error = $"history must be 0-{logs.Capacity}" }, statusCode: 400).ExecuteAsync(ctx);
            return;
        }
        var resumeFrom = long.TryParse(req.Headers["Last-Event-ID"].ToString(), NumberStyles.None, CultureInfo.InvariantCulture, out var lastId)
            ? lastId : (long?)null;

        res.Headers.ContentType = "text/event-stream";
        res.Headers.CacheControl = "no-cache";
        if (req.Protocol.StartsWith("HTTP/1", StringComparison.Ordinal))
            res.Headers.Connection = "keep-alive";
        res.Headers.AccessControlAllowOrigin = "*";
        ctx.Features.Get<IHttpResponseBodyFeature>()?.DisableBuffering();

        var (recent, live) = logs.Subscribe(resumeFrom ?? 0);
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ctx.RequestAborted, lifetime.ApplicationStopping);
        var ct = cts.Token;
        try
        {
            await WriteAsync(res, "data: " + JsonSerializer.Serialize(new { type = "connected", version = AppVersion.Value }, GoJson.Options) + "\n\n", ct);

            var backlog = recent.Where(e => e.Level >= minLevel);
            if (resumeFrom == null)
                backlog = backlog.TakeLast(history);
            foreach (var e in backlog)
                await WriteAsync(res, Format(e), ct);

            using var heartbeat = new PeriodicTimer(TemperatureStream.HeartbeatInterval, time);
            Task<bool>? next = null, beat = null;
            while (true)
            {
                next ??= live.Reader.WaitToReadAsync(ct).AsTask();
                beat ??= heartbeat.WaitForNextTickAsync(ct).AsTask();

                var done = await Task.WhenAny(next, beat);
                if (!await done)
                    return; // unsubscribed or timer disposed

                if (done == next)
                {
                    next = null;
                    while (live.Reader.TryRead(out var e))
                    {
                        if (e.Level >= minLevel)
                            await WriteAsync(res, Format(e), ct);
                    }
                }
                else
                {
                    beat = null;
                    await WriteAsync(res, ": heartbeat\n\n", ct);
                }
            }
        }
        catch (OperationCanceledException)
        {
            // Client disconnected or the app is shutting down.
        }
        catch (IOException)
        {
            // Write to a closed connection.
        }
        finally
        {
            logs.Unsubscribe(live);
        }
    }

    internal static bool TryParseLevel(string text, out LogLevel level)
    {
        switch (text.ToLowerInvariant())
        {
            case "":
            case "info":
            case "information":
                level = LogLevel.Information;
                return true;
            case "warn":
            case "warning":
                level = LogLevel.Warning;
                return true;
            case "error":
                level = LogLevel.Error;
                return true;
            case "critical":
                level = LogLevel.Critical;
                return true;
            default:
                level = LogLevel.None;
                return false;
        }
    }

    internal static string LevelName(LogLevel level) => level switch
    {
        LogLevel.Warning => "warning",
        LogLevel.Error => "error",
        LogLevel.Critical => "critical",
        _ => "info",
    };

    internal static string Format(LogEntry e)
    {
        var json = JsonSerializer.Serialize(new
        {
            category = e.Category,
            exception = e.Exception,
            id = e.Id,
            level = LevelName(e.Level),
            message = e.Message,
            time = e.Time.ToString("yyyy-MM-dd HH:mm:ss.fff", CultureInfo.InvariantCulture),
            type = "log",
        }, GoJson.Options);
        return $"id: {e.Id}\ndata: {json}\n\n";
    }

    private static async Task WriteAsync(HttpResponse res, string text, CancellationToken ct)
    {
        await res.Body.WriteAsync(Encoding.UTF8.GetBytes(text), ct);
        await res.Body.FlushAsync(ct);
    }
}
