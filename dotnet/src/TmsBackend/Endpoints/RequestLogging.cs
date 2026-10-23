using System.Diagnostics;

namespace TmsBackend.Endpoints;

/// <summary>One access-log line per request, like Fiber's logger middleware: "200 - 1.2ms GET /api/devices".</summary>
public static class RequestLogging
{
    public static IApplicationBuilder UseTmsRequestLogging(this IApplicationBuilder app)
    {
        var logger = app.ApplicationServices.GetRequiredService<ILoggerFactory>().CreateLogger("TmsBackend.Http");
        return app.Use(async (ctx, next) =>
        {
            var started = Stopwatch.GetTimestamp();
            try
            {
                await next(ctx);
            }
            finally
            {
                logger.LogInformation("{Status} - {Elapsed:0.0}ms {Method} {Path}{Query}",
                    ctx.Response.StatusCode, Stopwatch.GetElapsedTime(started).TotalMilliseconds,
                    ctx.Request.Method, ctx.Request.Path, ctx.Request.QueryString);
            }
        });
    }
}
