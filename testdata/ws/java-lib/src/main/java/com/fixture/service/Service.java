package com.fixture.service;

import com.fixture.store.Store;
import java.util.*;
import org.springframework.beans.factory.annotation.Value;

import static java.util.Objects.requireNonNull;

/** Service looks keys up in a store. */
public class Service implements Comparable<Service> {
    private final Store store;

    @Value("${lib.mode}")
    private String mode;

    @Value("${lib.timeout:30}")
    private int timeout;

    public Service(Store store) {
        this.store = requireNonNull(store);
    }

    public String lookup(String key) {
        String found = store.get(key);
        if (found == null) {
            return fallback(key);
        }
        return found;
    }

    private static String fallback(String key) {
        return System.getenv("JAVALIB_HOME") + System.getProperty("user.home");
    }

    @Override
    public int compareTo(Service other) {
        return this.store.hashCode() - other.store.hashCode();
    }
}
