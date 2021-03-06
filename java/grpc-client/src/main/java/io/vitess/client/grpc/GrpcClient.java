/*
 * Copyright 2019 The Vitess Authors.

 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at

 *     http://www.apache.org/licenses/LICENSE-2.0

 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.vitess.client.grpc;

import com.google.common.util.concurrent.AsyncFunction;
import com.google.common.util.concurrent.Futures;
import com.google.common.util.concurrent.ListenableFuture;
import com.google.common.util.concurrent.MoreExecutors;

import io.grpc.CallCredentials;
import io.grpc.InternalWithLogId;
import io.grpc.ManagedChannel;
import io.grpc.StatusRuntimeException;
import io.vitess.client.Context;
import io.vitess.client.Proto;
import io.vitess.client.RpcClient;
import io.vitess.client.StreamIterator;
import io.vitess.proto.Query.QueryResult;
import io.vitess.proto.Vtgate;
import io.vitess.proto.Vtgate.ExecuteRequest;
import io.vitess.proto.Vtgate.ExecuteResponse;
import io.vitess.proto.Vtgate.StreamExecuteRequest;
import io.vitess.proto.Vtgate.StreamExecuteResponse;
import io.vitess.proto.Vtgate.VStreamResponse;
import io.vitess.proto.grpc.VitessGrpc;
import io.vitess.proto.grpc.VitessGrpc.VitessFutureStub;
import io.vitess.proto.grpc.VitessGrpc.VitessStub;

import org.joda.time.Duration;

import java.io.IOException;
import java.sql.SQLException;
import java.sql.SQLIntegrityConstraintViolationException;
import java.sql.SQLInvalidAuthorizationSpecException;
import java.sql.SQLNonTransientException;
import java.sql.SQLRecoverableException;
import java.sql.SQLSyntaxErrorException;
import java.sql.SQLTimeoutException;
import java.sql.SQLTransientException;
import java.util.concurrent.TimeUnit;

/**
 * GrpcClient is a gRPC-based implementation of Vitess RpcClient.
 */
public class GrpcClient implements RpcClient {
  private static final Duration DEFAULT_TIMEOUT = Duration.standardSeconds(30);

  private final ManagedChannel channel;
  private final String channelId;
  private final VitessStub asyncStub;
  private final VitessFutureStub futureStub;
  private final Duration timeout;

  public GrpcClient(ManagedChannel channel) {
    this.channel = channel;
    channelId = toChannelId(channel);
    asyncStub = VitessGrpc.newStub(channel);
    futureStub = VitessGrpc.newFutureStub(channel);
    timeout = DEFAULT_TIMEOUT;
  }

  public GrpcClient(ManagedChannel channel, Context context) {
    this.channel = channel;
    channelId = toChannelId(channel);
    asyncStub = VitessGrpc.newStub(channel);
    futureStub = VitessGrpc.newFutureStub(channel);
    timeout = getContextTimeoutOrDefault(context);
  }

  public GrpcClient(ManagedChannel channel, CallCredentials credentials, Context context) {
    this.channel = channel;
    channelId = toChannelId(channel);
    asyncStub = VitessGrpc.newStub(channel).withCallCredentials(credentials);
    futureStub = VitessGrpc.newFutureStub(channel).withCallCredentials(credentials);
    timeout = getContextTimeoutOrDefault(context);
  }

  private String toChannelId(ManagedChannel channel) {
    return channel instanceof InternalWithLogId
        ? ((InternalWithLogId) channel).getLogId().toString() : channel.toString();
  }

  @Override
  public void close() throws IOException {
    try {
      if (!channel.shutdown().awaitTermination(timeout.getStandardSeconds(), TimeUnit.SECONDS)) {
        // The channel failed to shut down cleanly within the specified window
        // Now we try hard shutdown
        channel.shutdownNow();
      }
    } catch (InterruptedException exc) {
      Thread.currentThread().interrupt();
    }

  }

  @Override
  public ListenableFuture<ExecuteResponse> execute(Context ctx, ExecuteRequest request)
      throws SQLException {
    return Futures.catchingAsync(getFutureStub(ctx).execute(request), Exception.class,
        new ExceptionConverter<ExecuteResponse>(), MoreExecutors.directExecutor());
  }

  @Override
  public ListenableFuture<Vtgate.ExecuteBatchResponse> executeBatch(Context ctx,
      Vtgate.ExecuteBatchRequest request) throws SQLException {
    return Futures.catchingAsync(getFutureStub(ctx).executeBatch(request), Exception.class,
        new ExceptionConverter<Vtgate.ExecuteBatchResponse>(), MoreExecutors.directExecutor());
  }

  @Override
  public StreamIterator<QueryResult> streamExecute(Context ctx, StreamExecuteRequest request)
      throws SQLException {
    GrpcStreamAdapter<StreamExecuteResponse, QueryResult> adapter =
        new GrpcStreamAdapter<StreamExecuteResponse, QueryResult>() {
          @Override
          QueryResult getResult(StreamExecuteResponse response) throws SQLException {
            return response.getResult();
          }
        };
    getAsyncStub(ctx).streamExecute(request, adapter);
    return adapter;
  }

  @Override
  public StreamIterator<Vtgate.VStreamResponse> getVStream(Context ctx,
      Vtgate.VStreamRequest vstreamRequest) {
    GrpcStreamAdapter<VStreamResponse, VStreamResponse> adapter =
        new GrpcStreamAdapter<VStreamResponse, VStreamResponse>() {
          @Override
          VStreamResponse getResult(VStreamResponse response) {
            return response;
          }
        };

    getAsyncStub(ctx).vStream(vstreamRequest, adapter);
    return adapter;
  }

  /**
   * Converts an exception from the gRPC framework into the appropriate {@link SQLException}.
   */
  static SQLException convertGrpcError(Throwable exc) {
    if (exc instanceof StatusRuntimeException) {
      StatusRuntimeException sre = (StatusRuntimeException) exc;

      int errno = Proto.getErrno(sre.getMessage());
      String sqlState = Proto.getSQLState(sre.getMessage());

      switch (sre.getStatus().getCode()) {
        case INVALID_ARGUMENT:
          return new SQLSyntaxErrorException(sre.toString(), sqlState, errno, sre);
        case DEADLINE_EXCEEDED:
          return new SQLTimeoutException(sre.toString(), sqlState, errno, sre);
        case ALREADY_EXISTS:
          return new SQLIntegrityConstraintViolationException(sre.toString(), sqlState, errno, sre);
        case UNAUTHENTICATED:
          return new SQLInvalidAuthorizationSpecException(sre.toString(), sqlState, errno, sre);
        case UNAVAILABLE:
          return new SQLTransientException(sre.toString(), sqlState, errno, sre);
        case ABORTED:
          return new SQLRecoverableException(sre.toString(), sqlState, errno, sre);
        default: // Covers e.g. UNKNOWN.
          String advice = "";
          if (exc.getCause() instanceof java.nio.channels.ClosedChannelException) {
            advice =
                "Failed to connect to vtgate. Make sure that vtgate is running and you are using "
                    + "the correct address. Details: ";
          }
          return new SQLNonTransientException(
              "gRPC StatusRuntimeException: " + advice + exc.toString(), sqlState, errno, exc);
      }
    }
    return new SQLNonTransientException("gRPC error: " + exc.toString(), exc);
  }

  static class ExceptionConverter<V> implements AsyncFunction<Exception, V> {

    @Override
    public ListenableFuture<V> apply(Exception exc) throws Exception {
      throw convertGrpcError(exc);
    }
  }

  private VitessStub getAsyncStub(Context ctx) {
    Duration timeout = ctx.getTimeout();
    if (timeout == null) {
      return asyncStub;
    }
    return asyncStub.withDeadlineAfter(timeout.getMillis(), TimeUnit.MILLISECONDS);
  }

  private VitessFutureStub getFutureStub(Context ctx) {
    Duration timeout = ctx.getTimeout();
    if (timeout == null) {
      return futureStub;
    }
    return futureStub.withDeadlineAfter(timeout.getMillis(), TimeUnit.MILLISECONDS);
  }

  @Override
  public String toString() {
    return String.format("[GrpcClient-%s channel=%s]",
        Integer.toHexString(this.hashCode()),
        channelId
    );
  }

  private static Duration getContextTimeoutOrDefault(Context context) {
    if (context.getTimeout() == null || context.getTimeout().getStandardSeconds() < 0) {
      return DEFAULT_TIMEOUT;
    }

    return context.getTimeout();
  }
}
