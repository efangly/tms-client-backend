using System.Globalization;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Http.Features;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Services;

namespace TmsBackend.Endpoints;

/// <summary>
/// GET /api/temperature-stream: Server-Sent Events (TemperatureStream in
/// handlers.go). Sends "connected" (with the build version) on open, "refresh"
/// after each poll-and-save, "temperature" after each alert check, and a
/// comment heartbeat every 30 s. Ends when the client leaves or the app stops.
/// </summary>
public static class TemperatureStream
{
    internal static TimeSpan HeartbeatInterval { get; set; } = TimeSpan.FromSeconds(30);

    public static async Task HandleAsync(HttpContext ctx, EventHub hub, IHostApplicationLifetime lifetime, TimeProvider time)
    {
        var res = ctx.Response;
        res.Headers.ContentType = "text/event-stream";
        res.Headers.CacheControl = "no-cache";
        if (ctx.Request.Protocol.StartsWith("HTTP/1", StringComparison.Ordinal))
            res.Headers.Connection = "keep-alive";
        res.Headers.AccessControlAllowOrigin = "*";
        ctx.Features.Get<IHttpResponseBodyFeature>()?.DisableBuffering();

        var saved = hub.Subscribe();
        var temps = hub.SubscribeTemperature();
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ctx.RequestAborted, lifetime.ApplicationStopping);
        var ct = cts.Token;
        try
        {
            // fiber.Map keys are written in sorted order.
            await WriteAsync(res, "data: " + JsonSerializer.Serialize(new { type = "connected", version = AppVersion.Value }, GoJson.Options) + "\n\n", ct);

            using var heartbeat = new PeriodicTimer(HeartbeatInterval, time);
            Task<bool>? savedWait = null, tempWait = null, beat = null;
            while (true)
            {
                savedWait ??= saved.Reader.WaitToReadAsync(ct).AsTask();
                tempWait ??= temps.Reader.WaitToReadAsync(ct).AsTask();
                beat ??= heartbeat.WaitForNextTickAsync(ct).AsTask();

                var done = await Task.WhenAny(savedWait, tempWait, beat);
                if (!await done)
                    return; // channel completed (unsubscribed) or timer disposed

                if (done == savedWait)
                {
                    savedWait = null;
                    while (saved.Reader.TryRead(out var e))
                        await WriteAsync(res, $"data: {{\"type\":\"refresh\",\"saved\":{e.Saved},\"errors\":{e.Errors}}}\n\n", ct);
                }
                else if (done == tempWait)
                {
                    tempWait = null;
                    while (temps.Reader.TryRead(out var events))
                    {
                        var json = JsonSerializer.Serialize(new
                        {
                            count = events.Count,
                            data = events,
                            lastUpdated = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture),
                            type = "temperature",
                        }, GoJson.Options);
                        await WriteAsync(res, $"data: {json}\n\n", ct);
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
            hub.Unsubscribe(saved);
            hub.UnsubscribeTemperature(temps);
        }
    }

    private static async Task WriteAsync(HttpResponse res, string text, CancellationToken ct)
    {
        await res.Body.WriteAsync(Encoding.UTF8.GetBytes(text), ct);
        await res.Body.FlushAsync(ct);
    }
}
