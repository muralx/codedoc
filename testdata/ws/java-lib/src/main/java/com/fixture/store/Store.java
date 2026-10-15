package com.fixture.store;

/**
 * Store is a key-value store.
 *
 * <p>Implementations decide where values live.
 */
public interface Store {
    /** Returns the value stored under key. */
    String get(String key);

    /**
     * Stores value under key.
     */
    void put(String key, String value);

    /** Default timeout in seconds. */
    int TIMEOUT = 30;
}
